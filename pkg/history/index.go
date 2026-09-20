package history

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/zigai/aht/pkg/registry"
)

const (
	// indexVersion is the schema version written by indexSchema.
	indexVersion = 2
	// indexInvalidSuffix names the retained copy of an unusable default cache.
	indexInvalidSuffix = ".invalid"
	// sqlitePrimaryCodeMask isolates a primary SQLite result code from extended codes.
	sqlitePrimaryCodeMask = 0xff
)

// The tools column records whether the file stores tool parts, not its identity:
// every history has exactly one row and one copy of each part.
const indexSchema = `
CREATE TABLE files (
 id INTEGER PRIMARY KEY, harness TEXT NOT NULL, path TEXT NOT NULL, tools INTEGER NOT NULL,
 stamp TEXT NOT NULL, checkpoint TEXT NOT NULL, issues TEXT NOT NULL, omitted INTEGER NOT NULL,
 UNIQUE(harness,path));
CREATE TABLE conversations (
 id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 metadata TEXT NOT NULL, cwd TEXT NOT NULL, root TEXT NOT NULL);
CREATE INDEX conversations_file ON conversations(file_id);
CREATE INDEX conversations_cwd ON conversations(cwd);
CREATE INDEX conversations_root ON conversations(root);
CREATE TABLE parts (
 id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 role TEXT NOT NULL, body TEXT NOT NULL, folded TEXT NOT NULL,
 message_id TEXT NOT NULL, line INTEGER NOT NULL, timestamp TEXT NOT NULL);
CREATE INDEX parts_conversation ON parts(conversation_id);
CREATE VIRTUAL TABLE parts_fts USING fts5(folded, content='parts', content_rowid='id', tokenize='trigram case_sensitive 1');
CREATE TRIGGER parts_insert AFTER INSERT ON parts BEGIN
 INSERT INTO parts_fts(rowid,folded) VALUES(new.id,new.folded); END;
CREATE TRIGGER parts_delete AFTER DELETE ON parts BEGIN
 INSERT INTO parts_fts(parts_fts,rowid,folded) VALUES('delete',old.id,old.folded); END;
PRAGMA user_version=2;
`

var (
	// errIndexUnavailable reports that the disposable history index cannot be
	// used. Callers must fall back to direct scanning instead of failing.
	errIndexUnavailable = errors.New("history index unavailable")
	errIndexSchema      = errors.New("unrecognized history index; choose a new IndexPath or remove the disposable AHT index")
	errIndexWAL         = errors.New("history index cannot use write-ahead logging")
)

type indexedFile struct {
	id                                       int64
	harness, path, stamp, checkpoint, issues string
	omitted                                  int
	tools                                    bool
}

type historyIndex struct {
	db          *sql.DB
	conn        *sql.Conn
	tx          *sql.Tx
	unavailable error
	files       map[string]indexedFile
	paths       map[string]Source
	candidates  map[int64]bool
	plans       map[int64]indexedMatches
	queries     *indexQueries
	seen        map[string]bool
}

// openHistoryIndex prepares the disposable search index. An empty path uses the
// default cache under [os.UserCacheDir], whose unusable database is retained as
// history-v1.sqlite.invalid and rebuilt once. An explicit path is never moved
// aside and reports a database it does not recognize as a hard error. Any other
// setup failure, and lock contention on any path, returns errIndexUnavailable.
func openHistoryIndex(ctx context.Context, path string) (*historyIndex, error) {
	cached := path == ""
	if cached {
		root, err := os.UserCacheDir()
		if err != nil {
			return nil, indexUnavailable(fmt.Errorf("locate history index: %w", err))
		}
		path = filepath.Join(root, "aht", "history-v1.sqlite")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, indexUnavailable(fmt.Errorf("resolve history index: %w", err))
	}
	index, err := openIndexDatabase(ctx, path)
	if err == nil {
		return index, nil
	}
	if !cached {
		// An explicit index keeps the database it was pointed at intact, so an
		// unrecognized schema stays visible instead of scanning around it.
		if errors.Is(err, errIndexSchema) {
			return nil, err
		}
		return nil, indexUnavailable(err)
	}
	if isIndexBusy(err) {
		return nil, indexUnavailable(err)
	}
	// The default cache is disposable: move the unusable database aside and
	// rebuild it once.
	invalid := path + indexInvalidSuffix
	if healErr := replaceInvalidIndex(path, invalid); healErr != nil {
		return nil, errors.Join(indexUnavailable(err), fmt.Errorf("replace invalid history index: %w", healErr))
	}
	if index, err = openIndexDatabase(ctx, path); err != nil {
		return nil, indexUnavailable(fmt.Errorf("rebuild history index: %w", err))
	}
	return index, nil
}

// openIndexDatabase opens an existing index database, creating an empty one when
// the file holds no tables yet.
func openIndexDatabase(ctx context.Context, path string) (*historyIndex, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create history index directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open history index: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close history index file: %w", err)
	}
	var uri url.URL
	uri.Scheme, uri.Path = "file", path
	uri.RawQuery = url.Values{
		"_txlock": {"immediate"},
		"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"},
	}.Encode()
	// One connection keeps refresh transactions serialized per process while WAL
	// lets other readers keep the last committed snapshot.
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open history index database: %w", err)
	}
	db.SetMaxOpenConns(1)
	index := new(historyIndex)
	index.db = db
	index.files, index.paths, index.seen = map[string]indexedFile{}, map[string]Source{}, map[string]bool{}
	index.candidates, index.plans = map[int64]bool{}, map[int64]indexedMatches{}
	if err = index.connect(ctx); err != nil {
		return nil, errors.Join(err, index.close())
	}
	if err = index.initialize(ctx); err != nil {
		return nil, errors.Join(err, index.close())
	}
	return index, nil
}

// connect pins the index's single connection: reads nest inside each other, so
// they must not compete for a pooled connection.
func (index *historyIndex) connect(ctx context.Context) error {
	conn, err := index.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect history index: %w", err)
	}
	index.conn = conn
	return nil
}

func (index *historyIndex) initialize(ctx context.Context) error {
	if err := index.ensureSchema(ctx); err != nil {
		return err
	}
	if err := index.enableWAL(ctx); err != nil {
		return err
	}
	if err := index.loadFiles(ctx); err != nil {
		if isIndexContentFailure(err) {
			return schemaFailure(err)
		}
		return err
	}
	return nil
}

// ensureSchema validates the on-disk schema, creating it when the file holds no
// tables at all.
func (index *historyIndex) ensureSchema(ctx context.Context) error {
	var version int
	if err := index.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		if isIndexContentFailure(err) {
			return schemaFailure(err)
		}
		return fmt.Errorf("read history index version: %w", err)
	}
	if version == indexVersion {
		return nil
	}
	if version != 0 {
		return errIndexSchema
	}
	tx, err := index.conn.BeginTx(ctx, nil)
	if err != nil {
		return index.contention(fmt.Errorf("create history index: %w", err))
	}
	if err = index.createSchema(ctx, tx); err != nil {
		return errors.Join(err, rollbackTx(tx))
	}
	if err = tx.Commit(); err != nil {
		return index.contention(fmt.Errorf("create history index: %w", err))
	}
	return nil
}

// createSchema creates the index schema inside the caller's write transaction:
// the emptiness and version checks happen under the write lock, so concurrent
// searches observe one creator instead of racing to create the same tables.
func (index *historyIndex) createSchema(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		if isIndexContentFailure(err) {
			return schemaFailure(err)
		}
		return fmt.Errorf("read history index version: %w", err)
	}
	switch {
	case version == indexVersion:
		return nil
	case version != 0:
		return errIndexSchema
	}
	var tables int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		if isIndexContentFailure(err) {
			return schemaFailure(err)
		}
		return fmt.Errorf("inspect history index: %w", err)
	}
	if tables != 0 {
		return errIndexSchema
	}
	if _, err := tx.ExecContext(ctx, indexSchema); err != nil {
		return fmt.Errorf("create history index: %w", err)
	}
	return nil
}

// enableWAL makes readers independent of the per-file refresh transactions.
// synchronous(NORMAL) is sufficient for a disposable index: a lost commit is
// rebuilt from native history, which is never in this database. The conversion
// happens after the schema is recognized, so a database this build does not own
// is never modified.
func (index *historyIndex) enableWAL(ctx context.Context) error {
	var mode string
	if err := index.conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return index.contention(fmt.Errorf("enable history index WAL: %w", err))
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("%w: journal mode %q", errIndexWAL, mode)
	}
	return nil
}

func (index *historyIndex) loadFiles(ctx context.Context) (err error) {
	rows, err := index.conn.QueryContext(ctx, "SELECT id,harness,path,stamp,checkpoint,issues,omitted,tools FROM files")
	if err != nil {
		return fmt.Errorf("read indexed histories: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var file indexedFile
		if err = rows.Scan(&file.id, &file.harness, &file.path, &file.stamp, &file.checkpoint, &file.issues, &file.omitted, &file.tools); err != nil {
			return fmt.Errorf("read indexed file: %w", err)
		}
		key := file.harness + "\x00" + file.path
		index.paths[key] = Source{Harness: registry.Harness(file.harness), Path: file.path}
		index.files[key] = file
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("read indexed files: %w", err)
	}
	return nil
}

// beginWrite starts the short transaction that refreshes one changed history.
// Read paths never open a transaction, so a warm search takes no write lock.
func (index *historyIndex) beginWrite(ctx context.Context) error {
	tx, err := index.conn.BeginTx(ctx, nil)
	if err != nil {
		return index.contention(err)
	}
	index.tx = tx
	return nil
}

// endWrite commits an open refresh transaction.
func (index *historyIndex) endWrite() error {
	tx := index.tx
	index.tx = nil
	if tx == nil {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return index.contention(fmt.Errorf("commit history index: %w", err))
	}
	return nil
}

// rollbackWrite discards an open refresh transaction, keeping the previous
// committed index visible.
func (index *historyIndex) rollbackWrite() error {
	tx := index.tx
	index.tx = nil
	if tx == nil {
		return nil
	}
	return rollbackTx(tx)
}

// contention records lock waits that timed out, making the index unusable for
// this search so callers fall back to scanning native history. Other errors are
// returned unchanged.
func (index *historyIndex) contention(err error) error {
	if !isIndexBusy(err) {
		return err
	}
	index.unavailable = errors.Join(index.unavailable, errIndexUnavailable, err)
	return index.unavailable
}

// commit removes rows of histories that vanished since they were indexed. A warm
// search has nothing to delete and therefore takes no write lock at all.
func (index *historyIndex) commit(ctx context.Context) error {
	if index.unavailable != nil {
		return index.unavailable
	}
	var vanished []Source
	for key, source := range index.paths {
		if index.seen[key] {
			continue
		}
		if _, err := os.Stat(source.Path); errors.Is(err, os.ErrNotExist) {
			vanished = append(vanished, source)
		}
	}
	if len(vanished) == 0 {
		return nil
	}
	slices.SortFunc(vanished, func(a, b Source) int {
		if byHarness := cmp.Compare(a.Harness, b.Harness); byHarness != 0 {
			return byHarness
		}
		return cmp.Compare(a.Path, b.Path)
	})
	if err := index.beginWrite(ctx); err != nil {
		return err
	}
	for _, source := range vanished {
		if _, err := index.tx.ExecContext(ctx, "DELETE FROM files WHERE harness=? AND path=?", source.Harness, source.Path); err != nil {
			return errors.Join(index.contention(fmt.Errorf("remove deleted indexed history: %w", err)), index.rollbackWrite())
		}
	}
	return index.endWrite()
}

func (index *historyIndex) close() error {
	var err error
	if index.queries != nil {
		err = index.queries.close()
	}
	err = errors.Join(err, index.rollbackWrite())
	if index.conn != nil {
		err = errors.Join(err, closeConn(index.conn))
	}
	if index.db != nil {
		err = errors.Join(err, index.db.Close())
	}
	if err != nil {
		return fmt.Errorf("close history index: %w", err)
	}
	return nil
}

func (index *historyIndex) reportIssues(s *search, source Source, file indexedFile) {
	var issues []Issue
	if err := json.Unmarshal([]byte(file.issues), &issues); err != nil {
		s.issue(source, file.path, fmt.Errorf("decode history index issues: %w", err))
		return
	}
	for _, issue := range issues {
		issue.Source = source
		s.recordIssue(issue)
	}
	s.result.OmittedIssues += file.omitted
}

// indexUnavailable reports a disabled optimization, keeping the underlying
// cause inspectable through [errors.Is].
func indexUnavailable(err error) error {
	return errors.Join(errIndexUnavailable, err)
}

// isIndexBusy reports lock contention, including extended busy codes.
func isIndexBusy(err error) bool {
	switch indexSQLiteCode(err) {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

// isIndexContentFailure reports a file whose contents cannot be the index, as
// opposed to a filesystem or locking failure.
func isIndexContentFailure(err error) bool {
	switch indexSQLiteCode(err) {
	case sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT:
		return true
	default:
		return false
	}
}

// indexSQLiteCode extracts the primary SQLite result code, or zero.
func indexSQLiteCode(err error) int {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return 0
	}
	return sqliteErr.Code() & sqlitePrimaryCodeMask
}

// schemaFailure reports a database that is not a usable index while keeping the
// underlying database error.
func schemaFailure(err error) error {
	return fmt.Errorf("%w: %w", errIndexSchema, err)
}

// rollbackTx discards an open transaction, tolerating one that already ended.
func rollbackTx(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("roll back history index: %w", err)
	}
	return nil
}

// closeConn returns a pinned connection to its pool, tolerating a closed one.
func closeConn(conn *sql.Conn) error {
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("close history index connection: %w", err)
	}
	return nil
}

// replaceInvalidIndex retains an unusable cache database under a fixed name and
// discards its journals, which belong to the moved database.
func replaceInvalidIndex(path, invalid string) error {
	if err := os.Rename(path, invalid); err != nil {
		return fmt.Errorf("rename unusable history index: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove unusable history index journal: %w", err)
		}
	}
	return nil
}

func cleanIndexDir(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func directorySQL(dir string) (string, []any) {
	if dir == "" {
		return "1", nil
	}
	dir = filepath.Clean(dir)
	prefix := strings.TrimRight(dir, string(filepath.Separator)) + string(filepath.Separator)
	upper := prefix[:len(prefix)-1] + string(filepath.Separator+1)
	return "(c.cwd=? OR (c.cwd>=? AND c.cwd<?) OR c.root=? OR (c.root>=? AND c.root<?))", []any{dir, prefix, upper, dir, prefix, upper}
}
