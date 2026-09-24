package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zigai/aht/internal/harness/catalog"

	"golang.org/x/sys/unix"
)

const hashChunkBytes = 64 << 10

type indexCheckpoint struct {
	Conversation Conversation `json:"conversation"`
	Identity     string       `json:"identity"`
	Extra        string       `json:"extra"`
	Digest       string       `json:"digest"`
	Size         int64        `json:"size"`
	Lines        int          `json:"lines"`
	Recognized   bool         `json:"recognized"`
	Complete     bool         `json:"complete"`
	Tools        bool         `json:"tools"`
}

type indexWriter struct {
	tx                     *sql.Tx
	fileID, conversationID int64
	insert                 *sql.Stmt
	err                    error
	checkpoint             indexCheckpoint
	resume                 *indexCheckpoint
	hash                   hash.Hash
	includeTools           bool
}

func stampFile(info os.FileInfo) string {
	id, changed := fileIdentity(info)
	return fmt.Sprintf("%s:%s:%d:%d:%d", id, changed, info.Size(), info.ModTime().UnixNano(), info.Mode())
}

func stampPath(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("%T:%v", err, err)
	}
	return stampFile(info)
}

func transcriptExtra(s *search, source Source, path string) string {
	if extra := catalog.TranscriptFor(source.Harness).Extra; extra != nil {
		return extra(path, s.sourceMetadata, stampPath)
	}
	return ""
}

// unchanged uses the same identity, ctime, size, mtime, mode and sidecar stamp as
// an opened transcript. Check effective read access as well: cached content must
// not hide permission failures, including ACLs or changed process credentials.
// A miss follows the normal open path, which rechecks the opened file's identity.
func (index *historyIndex) unchanged(ctx context.Context, s *search, source Source, path string, info os.FileInfo) bool {
	file, exists := index.files[string(source.Harness)+"\x00"+path]
	if !exists || !info.Mode().IsRegular() || (s.query.IncludeTools && !file.tools) {
		return false
	}
	stamp := stampFile(info) + "|" + transcriptExtra(s, source, path)
	if stamp != file.stamp || unix.Faccessat(unix.AT_FDCWD, path, unix.R_OK, unix.AT_EACCESS) != nil {
		return false
	}
	// The matching stamp guarantees visit will not invoke a refresh callback.
	if err := index.visit(ctx, s, source, path, stamp, nil); errors.Is(err, errIndexUnavailable) {
		s.indexErr = err
	}
	return true
}

func (index *historyIndex) transcript(ctx context.Context, s *search, source Source, path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		s.issue(source, path, err)
		return nil
	}
	extra := transcriptExtra(s, source, path)

	stamp := stampFile(info) + "|" + extra
	return index.visit(ctx, s, source, path, stamp, func(writer *indexWriter, reader *search, previous indexedFile) error {
		writer.checkpoint.Identity, _ = fileIdentity(info)
		writer.checkpoint.Extra = extra
		writer.checkpoint.Size = info.Size()
		if catalog.TranscriptFor(source.Harness).Document == nil && !strings.HasSuffix(path, ".zst") {
			if err := writer.tryAppend(ctx, file, previous, info); err != nil {
				return err
			}
		}
		if writer.resume == nil {
			if _, err := writer.tx.ExecContext(ctx, "DELETE FROM conversations WHERE file_id=?", writer.fileID); err != nil {
				return fmt.Errorf("replace indexed conversation: %w", err)
			}
		}
		reader.scanTranscript(ctx, source, path, file)
		after, err := file.Stat()
		if err != nil {
			return fmt.Errorf("check indexed transcript: %w", err)
		}
		if stampFile(after) != stampFile(info) {
			writer.checkpoint.Complete = false
		}
		return nil
	})
}

func (index *historyIndex) database(ctx context.Context, s *search, source Source, path string) error {
	// WAL writes need not change the main database. Include both journal forms
	// and refresh the database's projection when any of them changes.
	stamp := stampPath(path) + "|" + stampPath(path+"-wal") + "|" + stampPath(path+"-journal")
	return index.visit(ctx, s, source, path, stamp, func(writer *indexWriter, reader *search, _ indexedFile) error {
		if _, err := writer.tx.ExecContext(ctx, "DELETE FROM conversations WHERE file_id=?", writer.fileID); err != nil {
			return fmt.Errorf("replace indexed database: %w", err)
		}
		reader.scanDatabase(ctx, source, path)
		return nil
	})
}

// visit refreshes a changed history in its own write transaction and then
// reports its matches from the committed index. Lock contention returns
// errIndexUnavailable so the caller can scan native history directly.
func (index *historyIndex) visit(ctx context.Context, s *search, source Source, path, stamp string, refresh func(*indexWriter, *search, indexedFile) error) error {
	if index.unavailable != nil {
		return index.unavailable
	}
	if err := index.prepareQuery(ctx, s); err != nil {
		return index.report(s, source, path, err)
	}
	key := string(source.Harness) + "\x00" + path
	index.seen[key] = true
	file, exists := index.files[key]
	// A tools query upgrades a file that did not store tool content.
	changed := !exists || file.stamp != stamp || (s.query.IncludeTools && !file.tools)
	if changed {
		if err := index.refresh(ctx, s, source, path, stamp, &file, refresh); err != nil {
			return index.report(s, source, path, err)
		}
		index.files[key] = file
	}
	index.reportIssues(s, source, file)
	if err := index.matches(ctx, s, file, changed); err != nil {
		return index.report(s, source, path, err)
	}
	return nil
}

// report returns failures that disable the index, so the search can restart
// against native history, and records every other failure as a diagnostic.
func (index *historyIndex) report(s *search, source Source, path string, err error) error {
	if errors.Is(err, errIndexUnavailable) {
		return err
	}
	s.issue(source, path, err)
	return nil
}

// refresh replaces one changed history inside its own transaction, so concurrent
// readers keep the last committed snapshot instead of waiting for a whole search.
func (index *historyIndex) refresh(ctx context.Context, s *search, source Source, path, stamp string, file *indexedFile, read func(*indexWriter, *search, indexedFile) error) error {
	previous := *file
	if err := index.beginWrite(ctx); err != nil {
		return err
	}
	updated, err := index.writeRefresh(ctx, s, source, path, stamp, previous, read)
	if err != nil {
		return errors.Join(err, index.rollbackWrite())
	}
	if err := index.endWrite(); err != nil {
		return err
	}
	*file = updated
	return nil
}

func (index *historyIndex) writeRefresh(ctx context.Context, s *search, source Source, path, stamp string, previous indexedFile, read func(*indexWriter, *search, indexedFile) error) (indexedFile, error) {
	file, skip, err := index.prepareIndexedFile(ctx, s, source, path, stamp, previous)
	if err != nil || skip {
		return file, err
	}

	writer := new(indexWriter)
	writer.tx, writer.fileID = index.tx, file.id
	writer.includeTools = file.tools
	writer.checkpoint.Tools = file.tools
	insert, err := index.tx.PrepareContext(ctx, "INSERT INTO parts(conversation_id,role,body,folded,message_id,line,timestamp) VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return indexedFile{}, fmt.Errorf("prepare history indexing: %w", err)
	}
	writer.insert = insert
	defer func() { _ = writer.insert.Close() }()
	reader := new(search)
	reader.query.IncludeTools = file.tools
	reader.sourceMetadata, reader.writer = s.sourceMetadata, writer
	if err = read(writer, reader, file); err != nil {
		return indexedFile{}, err
	}
	if err = errors.Join(writer.err, ctx.Err()); err != nil {
		return indexedFile{}, fmt.Errorf("refresh history: %w", err)
	}
	return index.saveRefresh(ctx, source, path, stamp, file, reader)
}

func (index *historyIndex) prepareIndexedFile(ctx context.Context, s *search, source Source, path, stamp string, previous indexedFile) (indexedFile, bool, error) {
	var current indexedFile
	err := index.tx.QueryRowContext(ctx, "SELECT id,stamp,checkpoint,issues,omitted,tools FROM files WHERE harness=? AND path=?", source.Harness, path).Scan(
		&current.id, &current.stamp, &current.checkpoint, &current.issues, &current.omitted, &current.tools,
	)
	includeTools := previous.tools || s.query.IncludeTools
	switch {
	case err == nil:
		includeTools = current.tools || s.query.IncludeTools
		if current.stamp == stamp && (!s.query.IncludeTools || current.tools) {
			current.harness = string(source.Harness)
			current.path = path
			return current, true, nil
		}
		current.harness = string(source.Harness)
		current.path = path
		current.tools = includeTools
		return current, false, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, err := index.tx.ExecContext(ctx, "INSERT INTO files(harness,path,tools,stamp,checkpoint,issues,omitted) VALUES(?,?,?,'','','[]',0) ON CONFLICT(harness,path) DO UPDATE SET tools=excluded.tools", source.Harness, path, includeTools); err != nil {
			return indexedFile{}, false, index.contention(fmt.Errorf("add indexed history: %w", err))
		}
		id, err := index.indexedRow(ctx, source, path)
		if err != nil {
			return indexedFile{}, false, err
		}
		previous.id = id
		previous.tools = includeTools
		return previous, false, nil
	default:
		return indexedFile{}, false, fmt.Errorf("identify indexed history: %w", err)
	}
}

// indexedRow returns the row of one indexed history, visible to the open
// refresh transaction after its upsert.
func (index *historyIndex) indexedRow(ctx context.Context, source Source, path string) (int64, error) {
	var id int64
	if err := index.tx.QueryRowContext(ctx, "SELECT id FROM files WHERE harness=? AND path=?", source.Harness, path).Scan(&id); err != nil {
		return 0, fmt.Errorf("identify indexed history: %w", err)
	}
	return id, nil
}

func (index *historyIndex) saveRefresh(ctx context.Context, source Source, path, stamp string, previous indexedFile, reader *search) (indexedFile, error) {
	var file indexedFile
	writer := reader.writer
	if writer.resume != nil {
		var oldIssues []Issue
		if err := json.Unmarshal([]byte(previous.issues), &oldIssues); err != nil {
			return file, fmt.Errorf("decode previous index issues: %w", err)
		}
		// Existing diagnostics precede newly appended lines, preserving the cap.
		newIssues, newOmitted := reader.result.Issues, reader.result.OmittedIssues
		reader.result.Issues = oldIssues
		reader.result.OmittedIssues = previous.omitted
		for _, issue := range newIssues {
			reader.recordIssue(issue)
		}
		reader.result.OmittedIssues += newOmitted
	}
	checkpoint, err := json.Marshal(writer.checkpoint)
	if err != nil {
		return file, fmt.Errorf("encode index checkpoint: %w", err)
	}
	issues, err := json.Marshal(reader.result.Issues)
	if err != nil {
		return file, fmt.Errorf("encode index issues: %w", err)
	}
	file = indexedFile{id: previous.id, harness: string(source.Harness), path: path, stamp: stamp, checkpoint: string(checkpoint), issues: string(issues), omitted: reader.result.OmittedIssues, tools: writer.includeTools}
	if _, err = index.tx.ExecContext(ctx, "UPDATE files SET stamp=?,checkpoint=?,issues=?,omitted=?,tools=? WHERE id=?", file.stamp, file.checkpoint, file.issues, file.omitted, file.tools, file.id); err != nil {
		return file, index.contention(fmt.Errorf("save history checkpoint: %w", err))
	}
	return file, nil
}

func (writer *indexWriter) conversation(ctx context.Context) {
	if writer.err != nil || writer.conversationID != 0 {
		return
	}
	result, err := writer.tx.ExecContext(ctx, "INSERT INTO conversations(file_id,metadata,cwd,root) VALUES(?,'{}','','')", writer.fileID)
	if err == nil {
		writer.conversationID, err = result.LastInsertId()
	}
	if err != nil {
		writer.err = fmt.Errorf("index conversation: %w", err)
	}
}

func (writer *indexWriter) append(ctx context.Context, part Excerpt) {
	// Conversation metadata is captured for every recognized message; only tool
	// content storage is opt-in.
	if part.Role == "tool" && !writer.includeTools {
		return
	}
	writer.conversation(ctx)
	if writer.err != nil {
		return
	}
	_, err := writer.insert.ExecContext(ctx, writer.conversationID, part.Role, part.Text, fold(part.Text), part.MessageID, part.Line, part.Timestamp.Format(time.RFC3339Nano))
	if err != nil {
		writer.err = fmt.Errorf("index message text: %w", err)
	}
}

func (writer *indexWriter) finish(ctx context.Context, c Conversation) {
	if writer.err != nil {
		return
	}
	if c.SessionID == "" {
		if writer.conversationID != 0 {
			_, writer.err = writer.tx.ExecContext(ctx, "DELETE FROM conversations WHERE id=?", writer.conversationID)
		}
	} else {
		writer.conversation(ctx)
		if writer.err != nil {
			return
		}
		metadata, err := json.Marshal(c)
		if err != nil {
			writer.err = fmt.Errorf("encode indexed conversation: %w", err)
			return
		}
		_, writer.err = writer.tx.ExecContext(ctx, "UPDATE conversations SET metadata=?,cwd=?,root=? WHERE id=?", string(metadata), cleanIndexDir(c.CWD), cleanIndexDir(c.ProjectRoot), writer.conversationID)
	}
	writer.conversationID = 0
}

// resumable reports whether this checkpoint can be extended in place: the
// writer must append to the same identity with the same tool mode.
func (checkpoint indexCheckpoint) resumable(writer *indexWriter, info os.FileInfo) bool {
	return checkpoint.Complete && checkpoint.Size < info.Size() &&
		checkpoint.Identity == writer.checkpoint.Identity && checkpoint.Extra == writer.checkpoint.Extra &&
		checkpoint.Tools == writer.includeTools
}

func (writer *indexWriter) tryAppend(ctx context.Context, file *os.File, previous indexedFile, info os.FileInfo) error {
	writer.hash = sha256.New()
	var checkpoint indexCheckpoint
	if previous.checkpoint == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(previous.checkpoint), &checkpoint); err != nil {
		return fmt.Errorf("decode history checkpoint: %w", err)
	}
	// Resuming keeps the stored rows, so it is only valid for an identical tool
	// mode: a tools query must not inherit a projection without tool content.
	if !checkpoint.resumable(writer, info) {
		return nil
	}
	// Verify the entire retained prefix before reusing parsed state. A rewrite
	// followed by an append must not retain stale messages, even with equal edges.
	if err := hashPrefix(ctx, writer.hash, file, checkpoint.Size); err != nil {
		return fmt.Errorf("verify history prefix: %w", err)
	}
	if hex.EncodeToString(writer.hash.Sum(nil)) == checkpoint.Digest {
		var id int64
		err := writer.tx.QueryRowContext(ctx, "SELECT id FROM conversations WHERE file_id=? ORDER BY id LIMIT 1", writer.fileID).Scan(&id)
		if err == nil {
			writer.conversationID = id
			writer.resume = &checkpoint
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("resume indexed conversation: %w", err)
		}
	}
	writer.hash.Reset()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind history: %w", err)
	}
	return nil
}

// hashPrefix bounds cancellation latency while verifying retained JSONL bytes.
func hashPrefix(ctx context.Context, digest hash.Hash, file *os.File, size int64) error {
	for remaining := size; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("hash history: %w", err)
		}
		n, err := io.CopyN(digest, file, min(remaining, hashChunkBytes))
		if err != nil {
			return fmt.Errorf("read history prefix: %w", err)
		}
		remaining -= n
	}
	return nil
}
