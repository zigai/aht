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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"

	"golang.org/x/sys/unix"
)

const hashChunkBytes = 64 << 10

// errStaleResume reports that another process rewrote a history's index entry
// after this search parsed an append against the older checkpoint.
var errStaleResume = errors.New("history index changed while refreshing")

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

// bufferedConversation is one parsed conversation waiting to be written.
// resumed marks the conversation continued from an append checkpoint; dropped
// marks one that turned out to lack a native session identity.
type bufferedConversation struct {
	conversation Conversation
	parts        []Excerpt
	resumed      bool
	dropped      bool
}

// indexWriter collects one history's parsed content off the database
// connection, so parse workers run in parallel and the single writer only
// inserts finished files.
type indexWriter struct {
	conversations []bufferedConversation
	open          bool
	checkpoint    indexCheckpoint
	resume        *indexCheckpoint
	hash          hash.Hash
	includeTools  bool
}

// refreshJob parses one changed history. previous is the file's indexed state
// when the job was created, and resumeID its first conversation row, which an
// append resume extends.
type refreshJob struct {
	source       Source
	file         historyFile
	previous     indexedFile
	exists       bool
	resumeID     int64
	includeTools bool
	metadata     map[string]string
	done         chan refreshResult
}

// refreshResult is a parsed history. openErr means the file could not be opened
// and was not inspected; failure means it was inspected but not parsed.
type refreshResult struct {
	stamp     string
	unchanged bool
	writer    *indexWriter
	issues    []Issue
	omitted   int
	openErr   error
	failure   error
	err       error
}

// pendingFile keeps walk order: matches and diagnostics are reported in the
// order files were discovered, whichever worker parsed them. children are the
// file's child histories, refreshed separately and matched with it.
type pendingFile struct {
	file     historyFile
	job      *refreshJob
	children []pendingFile
}

// settledFile is a history whose index entry is ready to read; changed means
// this search stored it.
type settledFile struct {
	path    string
	changed bool
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

// databaseStamp includes both journal forms: WAL writes need not change the
// main database, so any of them changing refreshes the database's projection.
func databaseStamp(path string) string {
	return stampPath(path) + "|" + stampPath(path+"-wal") + "|" + stampPath(path+"-journal")
}

func transcriptExtra(metadata map[string]string, source Source, path string) string {
	if extra := catalog.TranscriptFor(source.Harness).Extra; extra != nil {
		return extra(path, metadata, stampPath)
	}
	return ""
}

func fileKey(source Source, path string) string { return string(source.Harness) + "\x00" + path }

// scanFiles refreshes and searches one source's histories. Unchanged files are
// answered from the index; changed files are parsed by workers and written by
// this goroutine in discovery order, each in its own short transaction.
func (index *historyIndex) scanFiles(ctx context.Context, s *search, source Source, files []historyFile, status *SourceStatus) {
	if index.unavailable != nil {
		s.indexErr = index.unavailable
		return
	}
	if s.mode != modeRefresh {
		if err := index.prepareQuery(ctx, s); err != nil {
			index.report(s, source, source.Path, err)
			return
		}
	}
	workers := min(maxScanWorkers, max(minScanWorkers, runtime.GOMAXPROCS(0)))
	jobs, wg := startRefreshWorkers(ctx, workers)
	progress := Progress{Source: source, Done: 0, Total: len(files), Refreshed: 0}
	var queue []pendingFile
	for _, file := range files {
		if ctx.Err() != nil || index.unavailable != nil || s.halted() {
			break
		}
		pending := index.pend(s, source, file, jobs)
		for _, child := range file.children {
			pending.children = append(pending.children, index.pend(s, source, child, jobs))
		}
		queue = append(queue, pending)
		if len(queue) > 2*workers {
			index.finish(ctx, s, source, queue[0], status, &progress)
			queue = queue[1:]
		}
	}
	close(jobs)
	for _, pending := range queue {
		index.finish(ctx, s, source, pending, status, &progress)
	}
	wg.Wait()
}

func startRefreshWorkers(ctx context.Context, workers int) (chan *refreshJob, *sync.WaitGroup) {
	jobs := make(chan *refreshJob)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for job := range jobs {
				job.done <- parseRefresh(ctx, job)
			}
		}()
	}
	return jobs, &wg
}

// unchanged uses the same identity, ctime, size, mtime, mode and sidecar stamp as
// an opened transcript. Check effective read access as well: cached content must
// not hide permission failures, including ACLs or changed process credentials.
// A miss is parsed, which rechecks the opened file's identity.
func (index *historyIndex) unchanged(s *search, source Source, file historyFile) bool {
	stored, exists := index.files[fileKey(source, file.path)]
	if !exists || (s.query.IncludeTools && !stored.tools) {
		return false
	}
	if file.database {
		return databaseStamp(file.path) == stored.stamp
	}
	info, err := file.stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stamp := stampFile(info) + "|" + transcriptExtra(s.sourceMetadata, source, file.path)
	return stamp == stored.stamp && unix.Faccessat(unix.AT_FDCWD, file.path, unix.R_OK, unix.AT_EACCESS) == nil
}

func (index *historyIndex) newJob(s *search, source Source, file historyFile) *refreshJob {
	previous, exists := index.files[fileKey(source, file.path)]
	resumeID := previous.resumeID
	if file.database {
		resumeID = 0
	}
	return &refreshJob{source: source, file: file, previous: previous, exists: exists, resumeID: resumeID, includeTools: s.query.IncludeTools, metadata: s.sourceMetadata, done: make(chan refreshResult, 1)}
}

func (index *historyIndex) pend(s *search, source Source, file historyFile, jobs chan<- *refreshJob) pendingFile {
	pending := pendingFile{file: file, job: nil, children: nil}
	if !index.unchanged(s, source, file) {
		pending.job = index.newJob(s, source, file)
		jobs <- pending.job
	}
	return pending
}

// finish handles one history and its children in discovery order, storing
// each changed file before reading matches.
func (index *historyIndex) finish(ctx context.Context, s *search, source Source, pending pendingFile, status *SourceStatus, progress *Progress) {
	defer func() {
		progress.Done++
		s.report(*progress)
	}()
	parent, ok := index.settle(ctx, s, source, pending, status, progress)
	var children []settledFile
	for _, child := range pending.children {
		if settled, ok := index.settle(ctx, s, source, child, status, progress); ok {
			children = append(children, settled)
		}
	}
	if !ok {
		for _, child := range children {
			index.visit(ctx, s, source, child, nil)
		}
		return
	}
	index.visit(ctx, s, source, parent, children)
}

// settle stores one changed history and reports whether its index entry can be
// read.
func (index *historyIndex) settle(ctx context.Context, s *search, source Source, pending pendingFile, status *SourceStatus, progress *Progress) (settledFile, bool) {
	path := pending.file.path
	settled := settledFile{path: path, changed: false}
	if pending.job == nil {
		status.Files++
		return settled, true
	}
	result := <-pending.job.done
	if result.openErr != nil {
		s.issue(source, path, result.openErr)
		return settled, false
	}
	status.Files++
	if result.failure != nil {
		s.issue(source, path, result.failure)
		return settled, false
	}
	if result.unchanged {
		return settled, true
	}
	if result.err != nil {
		index.report(s, source, path, result.err)
		return settled, false
	}
	if index.unavailable != nil {
		s.indexErr = index.unavailable
		return settled, false
	}
	file, err := index.store(ctx, pending.job, result)
	if errors.Is(err, errStaleResume) {
		retry := *pending.job
		retry.previous, retry.resumeID = file, 0
		result = parseRefresh(ctx, &retry)
		if err = errors.Join(result.err, result.failure, result.openErr); err == nil && !result.unchanged {
			file, err = index.store(ctx, &retry, result)
		}
	}
	if err != nil {
		index.report(s, source, path, err)
		return settled, false
	}
	index.files[fileKey(source, path)] = file
	progress.Refreshed++
	settled.changed = true
	return settled, true
}

// visit reports the diagnostics of a stored history and its children, then
// reads their matches from the committed index.
func (index *historyIndex) visit(ctx context.Context, s *search, source Source, parent settledFile, children []settledFile) {
	var group []groupFile
	for _, settled := range append([]settledFile{parent}, children...) {
		key := fileKey(source, settled.path)
		index.seen[key] = true
		file, ok := index.files[key]
		if !ok {
			continue
		}
		index.reportIssues(s, source, file)
		group = append(group, groupFile{file: file, changed: settled.changed})
	}
	if len(group) == 0 || group[0].file.path != parent.path {
		for _, member := range group {
			index.visitGroup(ctx, s, source, []groupFile{member})
		}
		return
	}
	index.visitGroup(ctx, s, source, group)
}

func (index *historyIndex) visitGroup(ctx context.Context, s *search, source Source, group []groupFile) {
	if err := index.matches(ctx, s, group); err != nil {
		index.report(s, source, group[0].file.path, err)
	}
}

// report keeps failures that disable the index, so the search can restart
// against native history, and records every other failure as a diagnostic.
func (index *historyIndex) report(s *search, source Source, path string, err error) {
	if errors.Is(err, ErrIndexUnavailable) {
		s.indexErr = errors.Join(s.indexErr, err)
		return
	}
	s.issue(source, path, err)
}

// parseRefresh reads one history into an indexWriter without touching the
// database, so it can run on any worker.
func parseRefresh(ctx context.Context, job *refreshJob) refreshResult {
	includeTools := job.includeTools || (job.exists && job.previous.tools)
	writer := newIndexWriter(includeTools)
	reader := new(search)
	reader.mode = modeRefresh
	reader.query.IncludeTools = includeTools
	reader.sourceMetadata = job.metadata
	reader.writer = writer
	reader.reset()
	var result refreshResult
	var parsed bool
	if job.file.database {
		result, parsed = parseDatabaseRefresh(ctx, job, reader)
	} else {
		result, parsed = parseTranscriptRefresh(ctx, job, reader, writer)
	}
	if !parsed {
		return result
	}
	if err := ctx.Err(); err != nil {
		result.err = fmt.Errorf("refresh history: %w", err)
		return result
	}
	result.writer = writer
	result.issues = append(reader.result.Issues, result.issues...)
	result.omitted = reader.result.OmittedIssues
	return result
}

func (job *refreshJob) isCurrent(stamp string) bool {
	return job.exists && stamp == job.previous.stamp && (!job.includeTools || job.previous.tools)
}

func parseDatabaseRefresh(ctx context.Context, job *refreshJob, reader *search) (refreshResult, bool) {
	var result refreshResult
	result.stamp = databaseStamp(job.file.path)
	if job.isCurrent(result.stamp) {
		result.unchanged = true
		return result, false
	}
	reader.scanDatabase(ctx, job.source, job.file.path)
	return result, true
}

func parseTranscriptRefresh(ctx context.Context, job *refreshJob, reader *search, writer *indexWriter) (refreshResult, bool) {
	var result refreshResult
	source, path := job.source, job.file.path
	file, err := job.file.open()
	if err != nil {
		result.openErr = err
		return result, false
	}
	result, ok := parseOpenTranscript(ctx, job, reader, writer, file)
	if err := file.Close(); err != nil {
		result.issues = append(result.issues, Issue{Source: source, Path: path, Message: err.Error(), Record: false})
	}
	return result, ok
}

func parseOpenTranscript(ctx context.Context, job *refreshJob, reader *search, writer *indexWriter, file *os.File) (refreshResult, bool) {
	var result refreshResult
	source, path := job.source, job.file.path
	info, err := file.Stat()
	if err != nil {
		result.failure = err
		return result, false
	}
	extra := transcriptExtra(job.metadata, source, path)
	result.stamp = stampFile(info) + "|" + extra
	if job.isCurrent(result.stamp) {
		result.unchanged = true
		return result, false
	}
	writer.checkpoint.Identity, _ = fileIdentity(info)
	writer.checkpoint.Extra = extra
	writer.checkpoint.Size = info.Size()
	if catalog.TranscriptFor(source.Harness).Document == nil && !strings.HasSuffix(path, ".zst") {
		if err := writer.prepareAppendResume(ctx, file, job, info); err != nil {
			result.err = err
			return result, false
		}
	}
	if t := reader.scanTranscript(ctx, source, path, file); t != nil {
		reader.add(ctx, t)
	}
	after, err := file.Stat()
	if err != nil {
		result.err = fmt.Errorf("check indexed transcript: %w", err)
		return result, false
	}
	if stampFile(after) != stampFile(info) {
		writer.checkpoint.Complete = false
	}
	return result, true
}

// store replaces one changed history inside its own transaction, so concurrent
// readers keep the last committed snapshot instead of waiting for a whole search.
// When another process stored the same stamp first, its entry is reused.
func (index *historyIndex) store(ctx context.Context, job *refreshJob, result refreshResult) (indexedFile, error) {
	if err := index.beginWrite(ctx); err != nil {
		return indexedFile{}, err
	}
	file, err := index.writeRefresh(ctx, job, result)
	if err != nil {
		return file, errors.Join(err, index.rollbackWrite())
	}
	if err := index.endWrite(); err != nil {
		return indexedFile{}, err
	}
	return file, nil
}

func (index *historyIndex) writeRefresh(ctx context.Context, job *refreshJob, result refreshResult) (indexedFile, error) {
	source, path, writer := job.source, job.file.path, result.writer
	current, exists, err := index.storedFile(ctx, source, path)
	if err != nil {
		return indexedFile{}, err
	}
	if exists && writer.accepts(current, result.stamp) {
		return current, nil
	}
	if writer.staleResume(job, current, exists) {
		return current, errStaleResume
	}
	if !exists {
		if current, err = index.addFile(ctx, source, path, writer.includeTools); err != nil {
			return indexedFile{}, err
		}
	}
	if writer.resume == nil {
		if _, err := index.tx.ExecContext(ctx, "DELETE FROM conversations WHERE file_id=?", current.id); err != nil {
			return indexedFile{}, fmt.Errorf("replace indexed conversation: %w", err)
		}
	}
	first, err := index.writeConversations(ctx, current.id, job.resumeID, writer)
	if err != nil {
		return indexedFile{}, err
	}
	current.resumeID = first
	return index.saveRefresh(ctx, source, path, result, current)
}

func (writer *indexWriter) accepts(current indexedFile, stamp string) bool {
	return current.stamp == stamp && (!writer.includeTools || current.tools)
}

func (writer *indexWriter) staleResume(job *refreshJob, current indexedFile, exists bool) bool {
	return writer.resume != nil && (!exists || current.checkpoint != job.previous.checkpoint || current.resumeID != job.resumeID)
}

func (index *historyIndex) addFile(ctx context.Context, source Source, path string, includeTools bool) (indexedFile, error) {
	parent := catalog.TranscriptFor(source.Harness).Parent
	child := parent != nil && parent(path) != ""
	if _, err := index.tx.ExecContext(ctx, "INSERT INTO files(harness,path,tools,child,stamp,checkpoint,issues,omitted) VALUES(?,?,?,?,'','','[]',0)", source.Harness, path, includeTools, child); err != nil {
		return indexedFile{}, index.contention(fmt.Errorf("add indexed history: %w", err))
	}
	current, _, err := index.storedFile(ctx, source, path)
	if err != nil {
		return indexedFile{}, err
	}
	return current, nil
}

// storedFile reads a history's committed entry inside the open transaction.
func (index *historyIndex) storedFile(ctx context.Context, source Source, path string) (indexedFile, bool, error) {
	var file indexedFile
	err := index.tx.QueryRowContext(ctx, "SELECT id,"+firstConversationSQL+",stamp,checkpoint,issues,omitted,tools FROM files f WHERE harness=? AND path=?", source.Harness, path).Scan(
		&file.id, &file.resumeID, &file.stamp, &file.checkpoint, &file.issues, &file.omitted, &file.tools,
	)
	file.harness, file.path = string(source.Harness), path
	switch {
	case err == nil:
		return file, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return file, false, nil
	default:
		return file, false, index.contention(fmt.Errorf("identify indexed history: %w", err))
	}
}

// writeConversations stores the buffered conversations and returns the first
// conversation row of the history afterwards, or zero.
func (index *historyIndex) writeConversations(ctx context.Context, fileID, resumeID int64, writer *indexWriter) (int64, error) {
	insert, err := index.tx.PrepareContext(ctx, "INSERT INTO parts(conversation_id,role,body,folded,message_id,line,timestamp) VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return 0, fmt.Errorf("prepare history indexing: %w", err)
	}
	defer func() { _ = insert.Close() }()
	var first int64
	if writer.resume != nil {
		first = resumeID
	}
	for _, buffered := range writer.conversations {
		if buffered.dropped {
			if buffered.resumed {
				if _, err := index.tx.ExecContext(ctx, "DELETE FROM conversations WHERE id=?", resumeID); err != nil {
					return 0, fmt.Errorf("remove indexed conversation: %w", err)
				}
				first = 0
			}
			continue
		}
		id, err := index.upsertConversation(ctx, fileID, resumeID, buffered)
		if err != nil {
			return 0, err
		}
		if first == 0 {
			first = id
		}
		if err := index.insertParts(ctx, insert, id, buffered.parts); err != nil {
			return 0, err
		}
	}
	return first, nil
}

func (index *historyIndex) upsertConversation(ctx context.Context, fileID, resumeID int64, buffered bufferedConversation) (int64, error) {
	c := buffered.conversation
	metadata, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("encode indexed conversation: %w", err)
	}
	columns := []any{string(metadata), cleanIndexDir(c.CWD), cleanIndexDir(c.ProjectRoot), startedColumn(c), activeColumn(c), c.Messages, c.GitBranch}
	id := resumeID
	if buffered.resumed {
		_, err = index.tx.ExecContext(ctx, "UPDATE conversations SET metadata=?,cwd=?,root=?,started=?,active=?,messages=?,branch=? WHERE id=?", append(columns, id)...)
	} else {
		var inserted sql.Result
		inserted, err = index.tx.ExecContext(ctx, "INSERT INTO conversations(file_id,metadata,cwd,root,started,active,messages,branch) VALUES(?,?,?,?,?,?,?,?)", append([]any{fileID}, columns...)...)
		if err == nil {
			id, err = inserted.LastInsertId()
		}
	}
	if err != nil {
		return 0, index.contention(fmt.Errorf("index conversation: %w", err))
	}
	return id, nil
}

func (index *historyIndex) insertParts(ctx context.Context, insert *sql.Stmt, conversationID int64, parts []Excerpt) error {
	for _, part := range parts {
		if _, err := insert.ExecContext(ctx, conversationID, part.Role, part.Text, fold(part.Text), part.MessageID, part.Line, part.Timestamp.Format(time.RFC3339Nano)); err != nil {
			return index.contention(fmt.Errorf("index message text: %w", err))
		}
	}
	return nil
}

func startedColumn(c Conversation) int64 {
	if started := activeSince(c); !started.IsZero() {
		return started.Unix()
	}
	return unknownStarted
}

func activeColumn(c Conversation) int64 {
	if active := activeUntil(c); !active.IsZero() {
		return active.Unix()
	}
	return unknownActive
}

func (index *historyIndex) saveRefresh(ctx context.Context, source Source, path string, result refreshResult, previous indexedFile) (indexedFile, error) {
	writer := result.writer
	issues, omitted := result.issues, result.omitted
	if writer.resume != nil {
		var oldIssues []Issue
		if err := json.Unmarshal([]byte(previous.issues), &oldIssues); err != nil {
			return indexedFile{}, fmt.Errorf("decode previous index issues: %w", err)
		}
		// Existing diagnostics precede newly appended lines, preserving the cap.
		merged := new(search)
		merged.reset()
		merged.result.Issues = oldIssues
		merged.result.OmittedIssues = previous.omitted
		for _, issue := range issues {
			merged.recordIssue(issue)
		}
		issues, omitted = merged.result.Issues, merged.result.OmittedIssues+omitted
	}
	checkpoint, err := json.Marshal(writer.checkpoint)
	if err != nil {
		return indexedFile{}, fmt.Errorf("encode index checkpoint: %w", err)
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		return indexedFile{}, fmt.Errorf("encode index issues: %w", err)
	}
	file := indexedFile{id: previous.id, resumeID: previous.resumeID, harness: string(source.Harness), path: path, stamp: result.stamp, checkpoint: string(checkpoint), issues: string(encoded), omitted: omitted, tools: writer.includeTools}
	if _, err = index.tx.ExecContext(ctx, "UPDATE files SET stamp=?,checkpoint=?,issues=?,omitted=?,tools=? WHERE id=?", file.stamp, file.checkpoint, file.issues, file.omitted, file.tools, file.id); err != nil {
		return file, index.contention(fmt.Errorf("save history checkpoint: %w", err))
	}
	return file, nil
}

func newIndexWriter(includeTools bool) *indexWriter {
	var conversation Conversation
	checkpoint := indexCheckpoint{Conversation: conversation, Identity: "", Extra: "", Digest: "", Size: 0, Lines: 0, Recognized: false, Complete: false, Tools: includeTools}
	return &indexWriter{conversations: nil, open: false, checkpoint: checkpoint, resume: nil, hash: nil, includeTools: includeTools}
}

func (writer *indexWriter) current() *bufferedConversation {
	if !writer.open {
		resumed := writer.resume != nil && len(writer.conversations) == 0
		var conversation Conversation
		writer.conversations = append(writer.conversations, bufferedConversation{conversation: conversation, parts: nil, resumed: resumed, dropped: false})
		writer.open = true
	}
	return &writer.conversations[len(writer.conversations)-1]
}

func (writer *indexWriter) append(part Excerpt) {
	// Conversation metadata is captured for every recognized message; only tool
	// content storage is opt-in.
	if part.Role == "tool" && !writer.includeTools {
		return
	}
	buffered := writer.current()
	buffered.parts = append(buffered.parts, part)
}

func (writer *indexWriter) finish(c Conversation) {
	if c.SessionID == "" && !writer.open && (writer.resume == nil || len(writer.conversations) > 0) {
		return
	}
	buffered := writer.current()
	buffered.conversation = c
	buffered.dropped = c.SessionID == ""
	writer.open = false
}

// resumable reports whether this checkpoint can be extended in place: the
// writer must append to the same identity with the same tool mode.
func (checkpoint indexCheckpoint) resumable(writer *indexWriter, info os.FileInfo) bool {
	return checkpoint.Complete && checkpoint.Size < info.Size() &&
		checkpoint.Identity == writer.checkpoint.Identity && checkpoint.Extra == writer.checkpoint.Extra &&
		checkpoint.Tools == writer.includeTools
}

func (writer *indexWriter) prepareAppendResume(ctx context.Context, file *os.File, job *refreshJob, info os.FileInfo) error {
	writer.hash = sha256.New()
	var checkpoint indexCheckpoint
	if job.previous.checkpoint == "" || job.resumeID == 0 {
		return nil
	}
	if err := json.Unmarshal([]byte(job.previous.checkpoint), &checkpoint); err != nil {
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
		writer.resume = &checkpoint
		return nil
	}
	writer.hash.Reset()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind history: %w", err)
	}
	return nil
}

// hashPrefix bounds cancellation latency while verifying retained JSONL bytes.
func hashPrefix(ctx context.Context, digest hash.Hash, file *os.File, size int64) error {
	buffer := make([]byte, min(max(size, 0), hashChunkBytes))
	reader := io.LimitedReader{R: file, N: size}
	for remaining := size; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("hash history: %w", err)
		}
		reader.N = min(remaining, hashChunkBytes)
		expected := reader.N
		n, err := io.CopyBuffer(digest, &reader, buffer)
		if err == nil && n < expected {
			err = io.EOF
		}
		if err != nil {
			return fmt.Errorf("read history prefix: %w", err)
		}
		remaining -= n
	}
	return nil
}
