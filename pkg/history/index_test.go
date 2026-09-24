package history_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/pkg/history"
	"github.com/zigai/aht/pkg/registry"
)

// indexSchemaVersion is the on-disk schema version the index must write.
const indexSchemaVersion = 2

func indexedFixture(t *testing.T) (history.Catalog, string) {
	t.Helper()
	root := t.TempDir()
	path := writeHistory(t, root, "session.jsonl", treeHistory)
	return history.Catalog{Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}, path
}

func requireIndexedMatches(t *testing.T, c history.Catalog, q history.Query, count int) history.Result {
	t.Helper()
	result, err := c.Search(t.Context(), q)
	if err != nil || len(result.Matches) != count {
		t.Fatalf("search %q: matches=%d want=%d issues=%#v error=%v", q.Text, len(result.Matches), count, result.Issues, err)
	}
	return result
}

func appendHistory(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(body)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestIndexRefreshesAppendAndRewrite(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	appendHistory(t, path, `{"type":"message","id":"new","message":{"role":"user","content":"appended token"}}`+"\n")
	result := requireIndexedMatches(t, c, history.Query{Text: "appended token"}, 1)
	if result.Matches[0].Excerpts[0].Line != 7 {
		t.Fatalf("appended line = %d", result.Matches[0].Excerpts[0].Line)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same inode, same length and restored mtime still invalidates via ctime.
	//nolint:gosec // G703: path belongs to the isolated t.TempDir fixture, not transcript input.
	if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "refresh", "changed")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "changed"}, 1)
	// A prefix rewrite followed by growth must not qualify as an append.
	rewritten := strings.ReplaceAll(string(data), "Refresh", "Removed") + `{"type":"session_info","name":"rewritten"}` + "\n"
	//nolint:gosec // G703: path belongs to the isolated t.TempDir fixture, not transcript input.
	if err = os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "Removed"}, 1)
}

func TestIndexRefreshesReplacementTruncationAndDeletion(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	replacement := writeHistory(t, t.TempDir(), "replacement", strings.ReplaceAll(treeHistory, "native-session", "replacement"))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	result := requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	if result.Matches[0].Conversation.SessionID != "replacement" {
		t.Fatal("atomic replacement retained old identity")
	}
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"empty"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 0)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 0)
	db := openTestIndex(t, c.IndexPath)
	var files int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&files); err != nil || files != 0 {
		t.Fatalf("deleted history retained: count=%d err=%v", files, err)
	}
}

func TestIndexRechecksIncompleteLastLineAndRetainsDiagnostics(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"unfinished`)
	for range 2 {
		result, err := c.Search(t.Context(), history.Query{Text: "refresh"})
		if !errors.Is(err, history.ErrIncomplete) || len(result.Issues) != 1 || len(result.Matches) != 1 {
			t.Fatalf("cached partial result = %#v, %v", result, err)
		}
	}
	appendHistory(t, path, ` token"}}`+"\n")
	requireIndexedMatches(t, c, history.Query{Text: "unfinished token"}, 1)
	appendHistory(t, path, "invalid secret-sentinel\n")
	_, err := c.Search(t.Context(), history.Query{Text: "refresh"})
	if !errors.Is(err, history.ErrIncomplete) {
		t.Fatal(err)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"after error"}}`+"\n")
	result, err := c.Search(t.Context(), history.Query{Text: "after error"})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Issues) != 1 || len(result.Matches) != 1 {
		t.Fatalf("append diagnostics = %#v %v", result, err)
	}
	if strings.Contains(result.Issues[0].Message, "secret-sentinel") {
		t.Fatal("cached diagnostic exposed content")
	}
}

func TestIndexToolProjectionExcludesReasoningAndSystemContent(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	for _, tools := range []bool{false, true, false, true} {
		want := 0
		if tools {
			want = 1
		}
		requireIndexedMatches(t, c, history.Query{Text: "tool-only", IncludeTools: tools}, want)
		for _, text := range []string{"reasoning-only", "system-only"} {
			requireIndexedMatches(t, c, history.Query{Text: text, IncludeTools: tools}, 0)
		}
	}
	db := openTestIndex(t, c.IndexPath)
	var forbidden int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE instr(body,'reasoning-only')>0 OR instr(body,'system-only')>0").Scan(&forbidden); err != nil || forbidden != 0 {
		t.Fatalf("excluded text indexed: %d %v", forbidden, err)
	}
}

func TestIndexStoresSingleToolProjection(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	db := openTestIndex(t, c.IndexPath)
	requireIndexedMatches(t, c, history.Query{Text: "tool-only"}, 0)
	if files, toolFiles := indexStorage(t, db); files != 1 || toolFiles != 0 {
		t.Fatalf("tools-off index = %d files, %d storing tools", files, toolFiles)
	}
	plain := countParts(t, db, "role<>'tool'")
	if plain == 0 {
		t.Fatal("tools-off search stored no parts")
	}
	// A tools search upgrades the file in place: one row, one copy of each part.
	requireIndexedMatches(t, c, history.Query{Text: "tool-only", IncludeTools: true}, 1)
	if files, toolFiles := indexStorage(t, db); files != 1 || toolFiles != 1 {
		t.Fatalf("upgraded index = %d files, %d storing tools", files, toolFiles)
	}
	if stored := countParts(t, db, "role<>'tool'"); stored != plain {
		t.Fatalf("tools search stored %d non-tool rows, want %d", stored, plain)
	}
	if stored := countParts(t, db, "instr(body,'Refresh Token')>0"); stored != 1 {
		t.Fatalf("user text stored %d times", stored)
	}
	if stored := countParts(t, db, "role='tool'"); stored == 0 {
		t.Fatal("tools search stored no tool rows")
	}
	ids := partIDs(t, db)
	// A later tools-off search reuses the upgraded rows instead of rewriting them.
	requireIndexedMatches(t, c, history.Query{Text: "tool-only"}, 0)
	requireIndexedMatches(t, c, history.Query{Text: "Refresh Token"}, 1)
	if _, toolFiles := indexStorage(t, db); toolFiles != 1 {
		t.Fatal("tools-off search changed the storage mode")
	}
	if stored := countParts(t, db, "role='tool'"); stored == 0 {
		t.Fatal("tools-off search dropped the stored tool rows")
	}
	if diff := cmp.Diff(ids, partIDs(t, db)); diff != "" {
		t.Fatalf("tools-off search rewrote indexed rows (-before +after):\n%s", diff)
	}
}

// indexStorage reports the indexed file rows and how many of them store tool
// content.
func indexStorage(t *testing.T, db *sql.DB) (int, int) {
	t.Helper()
	var files, toolFiles int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*),coalesce(sum(tools),0) FROM files").Scan(&files, &toolFiles); err != nil {
		t.Fatal(err)
	}
	return files, toolFiles
}

// countParts counts stored parts matching a condition.
func countParts(t *testing.T, db *sql.DB, condition string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE "+condition).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIndexUsesWAL(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	db := openTestIndex(t, c.IndexPath)
	var mode string
	if err := db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal mode = %q, %v", mode, err)
	}
	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != indexSchemaVersion {
		t.Fatalf("user version = %d, %v", version, err)
	}
}

func TestIndexSearchesWhileWriteLocked(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	release := holdIndexWriteLock(t, c.IndexPath)
	defer release()
	// A warm search reads the last committed snapshot without a write lock: it
	// must not wait for the five-second busy timeout the lock imposes on writers.
	start := time.Now()
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("warm search waited %s on the index write lock", waited)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"locked append"}}`+"\n")
	result, err := c.Search(t.Context(), history.Query{Text: "locked append"})
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("locked refresh = %#v, %v", result, err)
	}
}

func TestIndexHealsUnusableDefaultCache(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeHistory(t, root, "session.jsonl", treeHistory)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}}
	path := filepath.Join(cache, "aht", "history-v1.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := openTestIndex(t, path)
	if _, err := foreign.ExecContext(t.Context(), "CREATE TABLE unrelated(value); INSERT INTO unrelated VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)

	healed := openTestIndex(t, path)
	var version, files int
	if err := healed.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != indexSchemaVersion {
		t.Fatalf("healed user version = %d, %v", version, err)
	}
	if err := healed.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&files); err != nil || files != 1 {
		t.Fatalf("healed index files = %d, %v", files, err)
	}
	// The unusable database is retained, so a second heal cannot have happened.
	retained := openTestIndex(t, path+".invalid")
	var kept string
	if err := retained.QueryRowContext(t.Context(), "SELECT value FROM unrelated").Scan(&kept); err != nil || kept != "keep" {
		t.Fatalf("retained cache = %q, %v", kept, err)
	}
}

func TestIndexRefreshesSidecarMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "session.messages.json", `{"sessionId":"s","messages":[{"role":"user","content":"needle"}]}`)
	manifest := writeHistory(t, root, "session.json", `{"session_id":"s","cwd":"/old","metadata":{"title":"old"}}`)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("cline"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, c, history.Query{Text: "needle", Dir: "/old"}, 1)
	if err := os.WriteFile(manifest, []byte(`{"session_id":"s","cwd":"/new","metadata":{"title":"new"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "needle", Dir: "/old"}, 0)
	result := requireIndexedMatches(t, c, history.Query{Text: "needle", Dir: "/new"}, 1)
	if result.Matches[0].Conversation.Title != "new" {
		t.Fatal("stale title")
	}
}

func TestIndexRefreshesSQLiteWALUpdatesAndDeletes(t *testing.T) {
	t.Parallel()
	db := databaseFixture(t, "state.db", `CREATE TABLE sessions(id,title,started_at,ended_at); CREATE TABLE messages(id,session_id,role,content,timestamp);
INSERT INTO sessions VALUES('s','title',1,2); INSERT INTO messages VALUES(1,'s','user','before',1);`)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("hermes"), Path: databasePath(t, db)}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, c, history.Query{Text: "before"}, 1)
	if _, err := db.ExecContext(t.Context(), "UPDATE messages SET content='after'"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "before"}, 0)
	requireIndexedMatches(t, c, history.Query{Text: "after"}, 1)
	if _, err := db.ExecContext(t.Context(), "DELETE FROM messages"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "after"}, 0)
}

func TestIndexConcurrentSearchAndCancellation(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			result, err := c.Search(t.Context(), history.Query{Text: "refresh"})
			if err != nil || len(result.Matches) != 1 {
				t.Errorf("concurrent search: %#v %v", result, err)
			}
		})
	}
	group.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Search(ctx, history.Query{Text: "refresh"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search: %v", err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
}

func openTestIndex(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestIndexRefreshFailureDoesNotReturnStaleMatches(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	db := openTestIndex(t, c.IndexPath)
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_refresh BEFORE INSERT ON parts BEGIN SELECT RAISE(ABORT,'test refresh failure'); END`); err != nil {
		t.Fatal(err)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"appended token"}}`+"\n")
	result, err := c.Search(t.Context(), history.Query{Text: "refresh"})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 || len(result.Issues) != 1 {
		t.Fatalf("failed refresh returned stale content: %#v, %v", result, err)
	}
	var parts int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE instr(body,'appended token')>0").Scan(&parts); err != nil || parts != 0 {
		t.Fatalf("failed refresh partially committed: %d, %v", parts, err)
	}
	if _, err := db.ExecContext(t.Context(), "DROP TRIGGER reject_refresh"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "appended token"}, 1)
}

func TestIndexRejectsForeignDatabaseAndCanBeRebuilt(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	db := openTestIndex(t, c.IndexPath)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE retained(value); INSERT INTO retained VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Search(t.Context(), history.Query{Text: "refresh"}); err == nil {
		t.Fatal("accepted foreign index")
	}
	var value string
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM retained").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("changed foreign database: %q, %v", value, err)
	}
	// Use a fresh path, then exercise rebuilding the disposable cache.
	c.IndexPath = filepath.Join(t.TempDir(), "index.sqlite")
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	info, err := os.Stat(c.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cache permissions: %v", info.Mode())
	}
	if err := os.Remove(c.IndexPath); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
}

func TestIndexRechecksKimiMetadataAndSourceSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manifest := writeHistory(t, root, "kimi.json", `{"work_dirs":[{"path":"/work/kimi-project","kaos":"local"}]}`)
	path := writeHistory(t, root, "sessions/aaec326b87de6c65cbc919cff0fa048e/native/context.jsonl", `{"role":"user","content":"refresh token"}`)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("kimi-code"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, c, history.Query{Text: "refresh", Dir: "/work/kimi-project"}, 1)
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Text: "refresh", Dir: "/work/kimi-project"}, 0)
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 1)
	c.Sources = []history.Source{}
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 0)
}

func TestIndexDeletesVanishedHistoriesForEveryToolMode(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Text: "tool-only", IncludeTools: true}, 1)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// One vanished history leaves no rows behind for either search mode.
	requireIndexedMatches(t, c, history.Query{Text: "refresh"}, 0)
	requireIndexedMatches(t, c, history.Query{Text: "tool-only", IncludeTools: true}, 0)
	db := openTestIndex(t, c.IndexPath)
	var files, parts int
	if err := db.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM files),(SELECT count(*) FROM parts)").Scan(&files, &parts); err != nil || files != 0 || parts != 0 {
		t.Fatalf("vanished history retained: files=%d parts=%d, %v", files, parts, err)
	}
}

// partIDs lists stored part rows so a test can detect a rewrite of unchanged
// history content.
func partIDs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT id FROM parts ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// holdIndexWriteLock takes the index write lock on a separate connection and
// returns a function that releases it.
func holdIndexWriteLock(t *testing.T, path string) func() {
	t.Helper()
	conn, err := openTestIndex(t, path).Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}
}
