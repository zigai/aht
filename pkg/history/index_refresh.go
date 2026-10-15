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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"

	"golang.org/x/sys/unix"
)

const (
	hashChunkBytes    = 64 << 10
	refreshBatchBytes = 256 << 10
)

// errStaleResume reports that another process rewrote a history's index entry
// after this search parsed an append against the older checkpoint.
var errStaleResume = errors.New("history index changed while refreshing")

type indexCheckpoint struct {
	Conversation Conversation `json:"conversation"`
	Identity     string       `json:"identity"`
	Extra        string       `json:"extra"`
	Projection   string       `json:"projection"`
	Digest       string       `json:"digest"`
	Size         int64        `json:"size"`
	Lines        int          `json:"lines"`
	Recognized   bool         `json:"recognized"`
	Complete     bool         `json:"complete"`
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
	reason        string
}

// refreshJob parses one changed history. previous is the file's indexed state
// when the job was created, and resumeID its first conversation row, which an
// append resume extends.
type refreshJob struct {
	source    Source
	file      historyFile
	previous  indexedFile
	exists    bool
	resumeID  int64
	prefilter *bytePrefilter
	filter    Filter
	metadata  map[string]string
	done      chan refreshResult
	rebuilt   bool
}

// refreshResult is a parsed history. openErr means the file could not be opened
// and was not inspected; failure means it was inspected but not parsed.
type refreshResult struct {
	stamp     string
	unchanged bool
	skipped   bool
	writer    *indexWriter
	issues    []Issue
	omitted   int
	openErr   error
	failure   error
	err       error
	bytes     int64
	parseTime time.Duration
	reason    string
}

// pendingFile keeps walk order: matches and diagnostics are reported in the
// order files were discovered, whichever worker parsed them. children are the
// file's child histories, refreshed separately and matched with it.
type pendingFile struct {
	file     historyFile
	filter   Filter
	job      *refreshJob
	children []pendingFile
}

// settledFile is a history whose index entry is ready to read; changed means
// this search stored it.
type settledFile struct {
	path    string
	changed bool
}

type refreshEntry struct {
	job    *refreshJob
	result refreshResult
}

type refreshBatch struct {
	entries []refreshEntry
	bytes   int
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
// this goroutine in discovery order. Refreshes batch writes; searches commit
// each file before reading its matches.
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
	var stats RefreshStats
	progress := Progress{Source: source, Done: 0, Total: len(files), Refreshed: 0, Refresh: stats, IndexUnavailable: false, Waiting: false}
	defer func() {
		s.result.Refresh = append(s.result.Refresh, SourceRefresh{Source: source, RefreshStats: progress.Refresh.snapshot()})
	}()
	var queue []pendingFile
	batch := new(refreshBatch)
	for _, file := range files {
		if ctx.Err() != nil || index.unavailable != nil || s.halted() {
			break
		}
		pending := index.pendFamily(ctx, s, source, file, jobs)
		queue = append(queue, pending)
		if len(queue) > 2*workers {
			index.finish(ctx, s, source, queue[0], status, &progress, batch)
			queue = queue[1:]
		}
	}
	close(jobs)
	for _, pending := range queue {
		index.finish(ctx, s, source, pending, status, &progress, batch)
	}
	wg.Wait()
	index.flushRefresh(ctx, s, batch, &progress)
}

func (index *historyIndex) pendFamily(ctx context.Context, s *search, source Source, file historyFile, jobs chan<- *refreshJob) pendingFile {
	filter := s.query.Filter
	prefilter := s.prefilterMembers(ctx, file)
	if len(file.children) > 0 && file.mayMatchTime(filter) {
		filter.Since = time.Time{}
	}
	pending := index.pend(s, source, file, jobs, prefilter, filter)
	for _, child := range file.children {
		pending.children = append(pending.children, index.pend(s, source, child, jobs, prefilter, filter))
	}
	return pending
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
	if !exists {
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
	return &refreshJob{source: source, file: file, previous: previous, exists: exists, resumeID: resumeID, prefilter: nil, filter: s.query.Filter, metadata: s.sourceMetadata, done: make(chan refreshResult, 1), rebuilt: index.rebuilt}
}

func (index *historyIndex) pend(s *search, source Source, file historyFile, jobs chan<- *refreshJob, prefilter bool, filter Filter) pendingFile {
	pending := pendingFile{file: file, filter: filter, job: nil, children: nil}
	if !index.unchanged(s, source, file) {
		pending.job = index.newJob(s, source, file)
		pending.job.filter = filter
		if prefilter {
			pending.job.prefilter = s.prefilter
		}
		jobs <- pending.job
	}
	return pending
}

// finish handles one history and its children in discovery order, settling
// each file before reading its committed matches.
func (index *historyIndex) finish(ctx context.Context, s *search, source Source, pending pendingFile, status *SourceStatus, progress *Progress, batch *refreshBatch) {
	defer func() {
		progress.Done++
		s.report(*progress)
	}()
	parent, ok := index.settle(ctx, s, source, pending, status, progress, batch)
	var children []settledFile
	for _, child := range pending.children {
		if settled, ok := index.settle(ctx, s, source, child, status, progress, batch); ok {
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

// settle refreshes or queues one history and reports whether its committed
// index entry can be read.
func (index *historyIndex) settle(ctx context.Context, s *search, source Source, pending pendingFile, status *SourceStatus, progress *Progress, batch *refreshBatch) (settledFile, bool) {
	path := pending.file.path
	settled := settledFile{path: path, changed: false}
	if pending.job == nil {
		if ctx.Err() != nil {
			return settled, false
		}
		status.Files++
		progress.Refresh.Checked++
		progress.Refresh.Unchanged++
		return settled, pending.file.mayMatchTime(pending.filter)
	}
	result := <-pending.job.done
	if ctx.Err() != nil {
		return settled, false
	}
	progress.Refresh.Checked++
	if result.openErr != nil {
		s.fileIssue(source, path, result.openErr)
		return settled, false
	}
	status.Files++
	if result.skipped {
		progress.Refresh.Skipped++
		s.reportIssues(path, result.issues, result.omitted, nil)
		return settled, false
	}
	if err := errors.Join(result.failure, result.err); err != nil {
		index.report(s, source, path, err)
		return settled, false
	}
	if result.unchanged {
		progress.Refresh.Unchanged++
		return settled, true
	}
	if index.unavailable != nil {
		s.indexErr = index.unavailable
		return settled, false
	}
	if s.mode == modeRefresh {
		bytes := result.writer.textBytes()
		if batch.bytes+bytes > refreshBatchBytes {
			index.flushRefresh(ctx, s, batch, progress)
		}
		batch.entries = append(batch.entries, refreshEntry{job: pending.job, result: result})
		batch.bytes += bytes
		if batch.bytes >= refreshBatchBytes {
			index.flushRefresh(ctx, s, batch, progress)
		}
		return settled, false
	}
	return index.settleParsed(ctx, s, pending.job, result, progress)
}

func (index *historyIndex) settleParsed(ctx context.Context, s *search, job *refreshJob, result refreshResult, progress *Progress) (settledFile, bool) {
	source, path := job.source, job.file.path
	settled := settledFile{path: path, changed: false}
	file, storeTime, err := index.storeParsed(ctx, job, &result)
	if result.skipped {
		progress.Refresh.Skipped++
		s.reportIssues(path, result.issues, result.omitted, nil)
		return settled, false
	}
	if err != nil {
		index.report(s, source, path, err)
		return settled, false
	}
	index.files[fileKey(source, path)] = file
	if result.unchanged {
		progress.Refresh.Unchanged++
	} else {
		progress.Refreshed++
		s.refreshed(source, path, result, storeTime, &progress.Refresh)
	}
	settled.changed = true
	return settled, true
}

func (writer *indexWriter) textBytes() int {
	var bytes int
	for _, conversation := range writer.conversations {
		if conversation.dropped {
			continue
		}
		for _, part := range conversation.parts {
			bytes += len(part.Text)
		}
	}
	return bytes
}

func (index *historyIndex) storeBatch(ctx context.Context, entries []refreshEntry) ([]indexedFile, error) {
	if err := index.beginWrite(ctx); err != nil {
		return nil, err
	}
	files := make([]indexedFile, 0, len(entries))
	for _, entry := range entries {
		file, err := index.writeRefresh(ctx, entry.job, entry.result)
		if err != nil {
			return nil, errors.Join(err, index.rollbackWrite(ctx))
		}
		files = append(files, file)
	}
	if err := index.endWrite(ctx); err != nil {
		return nil, err
	}
	return files, nil
}

func (index *historyIndex) flushRefresh(ctx context.Context, s *search, batch *refreshBatch, progress *Progress) {
	if len(batch.entries) == 0 {
		return
	}
	started := time.Now()
	files, err := index.storeBatch(ctx, batch.entries)
	if err == nil {
		progress.Refresh.StoreTime += time.Since(started)
	}
	for position, entry := range batch.entries {
		result := entry.result
		var file indexedFile
		var storeTime time.Duration
		if err == nil {
			file = files[position]
		} else {
			var retryErr error
			file, storeTime, retryErr = index.storeParsed(ctx, entry.job, &result)
			if retryErr != nil {
				index.report(s, entry.job.source, entry.job.file.path, retryErr)
				continue
			}
		}
		if result.skipped {
			progress.Refresh.Skipped++
			continue
		}
		key := fileKey(entry.job.source, entry.job.file.path)
		index.files[key], index.seen[key] = file, true
		index.reportIssues(s, entry.job.source, file, nil)
		if result.unchanged {
			progress.Refresh.Unchanged++
		} else {
			progress.Refreshed++
			s.refreshed(entry.job.source, entry.job.file.path, result, storeTime, &progress.Refresh)
		}
	}
	batch.entries, batch.bytes = nil, 0
	s.report(*progress)
}

// visit reads a stored history and its children from the committed index.
func (index *historyIndex) visit(ctx context.Context, s *search, source Source, parent settledFile, children []settledFile) {
	var group []groupFile
	for _, settled := range append([]settledFile{parent}, children...) {
		key := fileKey(source, settled.path)
		index.seen[key] = true
		file, ok := index.files[key]
		if !ok {
			continue
		}
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
		return
	}
	if err := index.reportGroupIssues(ctx, s, source, group); err != nil {
		index.report(s, source, group[0].file.path, err)
	}
}

func (index *historyIndex) reportGroupIssues(ctx context.Context, s *search, source Source, group []groupFile) error {
	if s.mode == modeRefresh {
		for _, member := range group {
			index.reportIssues(s, source, member.file, nil)
		}
		return nil
	}
	pending := false
	for _, member := range group {
		pending = pending || (member.file.issues != "[]" && member.file.issues != "") || member.file.omitted > 0
	}
	if !pending {
		return nil
	}
	selected, err := index.selectIssueMetadata(ctx, s, group)
	if err != nil {
		return err
	}
	for _, member := range group {
		index.reportIssues(s, source, member.file, selected)
	}
	return nil
}

func (index *historyIndex) selectIssueMetadata(ctx context.Context, s *search, group []groupFile) (map[string]bool, error) {
	fresh := slices.ContainsFunc(group, func(member groupFile) bool { return member.changed })
	parents, err := index.readConversations(ctx, group[0].file.id, fresh)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool)
	parentMetadata := make(map[string]indexedConversation, len(parents))
	for _, parent := range parents {
		c := parent.conversation
		selected[c.Path] = selected[c.Path] || s.issueConversationSelected(c, index.plans[parent.id].qualifies() && !index.excluded[parent.id])
		if _, exists := parentMetadata[c.SessionID]; !exists {
			parentMetadata[c.SessionID] = parent
		}
	}
	for _, member := range group[1:] {
		children, err := index.readConversations(ctx, member.file.id, fresh)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			owner := child
			if parent, exists := parentMetadata[child.conversation.SessionID]; exists {
				owner = parent
			}
			selected[child.conversation.Path] = selected[child.conversation.Path] || s.issueConversationSelected(owner.conversation, index.plans[owner.id].qualifies() && !index.excluded[owner.id])
		}
	}
	return selected, nil
}

// report keeps failures that disable the index, so the search can restart
// against native history, and records every other failure as a diagnostic.
func (index *historyIndex) report(s *search, source Source, path string, err error) {
	if errors.Is(err, ErrIndexUnavailable) {
		s.indexErr = errors.Join(s.indexErr, err)
		return
	}
	s.fileIssue(source, path, err)
}

// parseRefresh reads one history into an indexWriter without touching the
// database, so it can run on any worker.
func parseRefresh(ctx context.Context, job *refreshJob) refreshResult {
	if !job.file.mayMatchTime(job.filter) {
		var result refreshResult
		result.skipped = true
		return result
	}
	started := time.Now()
	writer := newIndexWriter()
	reader := new(search)
	reader.mode = modeRefresh
	reader.sourceMetadata = job.metadata
	reader.writer = writer
	reader.reset()
	buffer := newFileIssueBuffer()
	var result refreshResult
	var parsed bool
	if job.file.database {
		result, parsed = parseDatabaseRefresh(ctx, job, reader, buffer)
	} else {
		result, parsed = parseTranscriptRefresh(ctx, job, reader, writer, buffer)
	}
	if !parsed && !result.skipped {
		return result
	}
	if err := ctx.Err(); err != nil {
		result.err = fmt.Errorf("refresh history: %w", err)
		return result
	}
	reader.reportScopedIssues(buffer)
	result.issues = append(reader.result.Issues, result.issues...)
	result.omitted = reader.result.OmittedIssues
	if result.skipped {
		return result
	}
	result.writer = writer
	result.parseTime = time.Since(started)
	result.reason = writer.reason
	if result.reason == "" {
		result.reason = job.reparseReason()
	}
	return result
}

func (job *refreshJob) reparseReason() string {
	switch {
	case job.rebuilt:
		return "schema rebuild"
	case !job.exists:
		return "new file"
	default:
		return "changed file"
	}
}

func (job *refreshJob) isCurrent(stamp string) bool {
	return job.exists && stamp == job.previous.stamp
}

func parseDatabaseRefresh(ctx context.Context, job *refreshJob, reader *search, buffer *fileIssueBuffer) (refreshResult, bool) {
	var result refreshResult
	result.stamp = databaseStamp(job.file.path)
	if job.isCurrent(result.stamp) {
		result.unchanged = true
		return result, false
	}
	if info, err := os.Stat(job.file.path); err == nil {
		result.bytes = info.Size()
	}
	reader.scanDatabase(ctx, job.source, job.file.path, buffer)
	return result, true
}

func parseTranscriptRefresh(ctx context.Context, job *refreshJob, reader *search, writer *indexWriter, buffer *fileIssueBuffer) (refreshResult, bool) {
	var result refreshResult
	source, path := job.source, job.file.path
	file, err := job.file.open()
	if err != nil {
		result.openErr = err
		return result, false
	}
	result, ok := parseOpenTranscript(ctx, job, reader, writer, file, buffer)
	if result.skipped {
		reader.writer = nil
		reader.checkTranscriptFormat(ctx, source, path, file, buffer)
	}
	if err := file.Close(); err != nil {
		result.issues = append(result.issues, Issue{Source: source, Path: path, Message: err.Error(), Record: false})
	}
	return result, ok
}

func (job *refreshJob) lacksTerms(ctx context.Context, file *os.File) (bool, error) {
	if strings.HasSuffix(job.file.path, ".zst") {
		return false, nil
	}
	candidate, err := job.prefilter.matchesFile(ctx, file)
	return err == nil && !candidate, err
}

func parseOpenTranscript(ctx context.Context, job *refreshJob, reader *search, writer *indexWriter, file *os.File, buffer *fileIssueBuffer) (refreshResult, bool) {
	var result refreshResult
	source, path := job.source, job.file.path
	info, err := file.Stat()
	if err != nil {
		result.failure = err
		return result, false
	}
	extra := transcriptExtra(job.metadata, source, path)
	result.stamp = stampFile(info) + "|" + extra
	if result.skipped, result.err = job.lacksTerms(ctx, file); result.skipped || result.err != nil {
		return result, false
	}
	if job.isCurrent(result.stamp) {
		result.unchanged = true
		return result, false
	}
	if err := writer.prepareTranscript(ctx, job, file, info, extra); err != nil {
		result.err = err
		return result, false
	}
	result.bytes = info.Size()
	if writer.resume != nil {
		result.bytes -= writer.resume.Size
	}
	if t := reader.scanTranscript(ctx, source, path, file, buffer); t != nil {
		reader.add(ctx, t)
	}
	if err := writer.checkSnapshot(file, info); err != nil {
		result.err = err
		return result, false
	}
	return result, true
}

func (writer *indexWriter) prepareTranscript(ctx context.Context, job *refreshJob, file *os.File, info os.FileInfo, extra string) error {
	writer.checkpoint.Identity, _ = fileIdentity(info)
	writer.checkpoint.Extra = extra
	writer.checkpoint.Size = info.Size()
	if catalog.TranscriptFor(job.source.Harness).Document != nil || strings.HasSuffix(job.file.path, ".zst") {
		return nil
	}
	return writer.prepareAppendResume(ctx, file, job, info)
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
		return file, errors.Join(err, index.rollbackWrite(ctx))
	}
	if err := index.endWrite(ctx); err != nil {
		return indexedFile{}, err
	}
	return file, nil
}

func (index *historyIndex) storeParsed(ctx context.Context, job *refreshJob, result *refreshResult) (indexedFile, time.Duration, error) {
	started := time.Now()
	file, err := index.store(ctx, job, *result)
	elapsed := time.Since(started)
	if !errors.Is(err, errStaleResume) {
		return file, elapsed, err
	}
	retry := *job
	retry.previous, retry.resumeID = file, 0
	*result = parseRefresh(ctx, &retry)
	if err := errors.Join(result.err, result.failure, result.openErr); err != nil {
		return file, elapsed, err
	}
	if result.skipped || result.unchanged {
		return file, elapsed, nil
	}
	started = time.Now()
	file, err = index.store(ctx, &retry, *result)
	return file, elapsed + time.Since(started), err
}

func (index *historyIndex) writeRefresh(ctx context.Context, job *refreshJob, result refreshResult) (indexedFile, error) {
	source, path, writer := job.source, job.file.path, result.writer
	current, exists, err := index.storedFile(ctx, source, path)
	if err != nil {
		return indexedFile{}, err
	}
	if exists && current.stamp == result.stamp {
		return current, nil
	}
	if writer.staleResume(job, current, exists) {
		return current, errStaleResume
	}
	if !exists {
		if current, err = index.addFile(ctx, source, path); err != nil {
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

func (writer *indexWriter) staleResume(job *refreshJob, current indexedFile, exists bool) bool {
	return writer.resume != nil && (!exists || current.checkpoint != job.previous.checkpoint || current.resumeID != job.resumeID)
}

func (index *historyIndex) addFile(ctx context.Context, source Source, path string) (indexedFile, error) {
	parent := catalog.TranscriptFor(source.Harness).Parent
	child := parent != nil && parent(path) != ""
	if _, err := index.tx.ExecContext(ctx, "INSERT INTO files(harness,path,child,stamp,checkpoint,issues,omitted) VALUES(?,?,?,'','','[]',0)", source.Harness, path, child); err != nil {
		return indexedFile{}, index.failure(fmt.Errorf("add indexed history: %w", err))
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
	err := index.tx.queryRow("SELECT id,"+firstConversationSQL+",stamp,checkpoint,issues,omitted FROM files f WHERE harness=? AND path=?", source.Harness, path).Scan(ctx,
		&file.id, &file.resumeID, &file.stamp, &file.checkpoint, &file.issues, &file.omitted,
	)
	file.harness, file.path = string(source.Harness), path
	switch {
	case err == nil:
		return file, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return file, false, nil
	default:
		return file, false, index.failure(fmt.Errorf("identify indexed history: %w", err))
	}
}

// writeConversations stores the buffered conversations and returns the first
// conversation row of the history afterwards, or zero.
func (index *historyIndex) writeConversations(ctx context.Context, fileID, resumeID int64, writer *indexWriter) (int64, error) {
	insert, err := index.tx.PrepareContext(ctx, "INSERT INTO parts(conversation_id,role,body,message_id,line,timestamp) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return 0, fmt.Errorf("prepare history indexing: %w", err)
	}
	defer func() { _ = insert.Close() }()
	fullText, err := index.tx.PrepareContext(ctx, "INSERT INTO parts_fts(rowid,folded) VALUES(?,?)")
	if err != nil {
		return 0, fmt.Errorf("prepare full-text indexing: %w", err)
	}
	defer func() { _ = fullText.Close() }()
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
		if err := index.insertParts(ctx, insert, fullText, id, buffered.parts); err != nil {
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
		return 0, index.failure(fmt.Errorf("index conversation: %w", err))
	}
	return id, nil
}

func (index *historyIndex) insertParts(ctx context.Context, insert, fullText *indexStatement, conversationID int64, parts []Excerpt) error {
	for _, part := range parts {
		inserted, err := insert.ExecContext(ctx, conversationID, part.Role, part.Text, part.MessageID, part.Line, part.Timestamp.Format(time.RFC3339Nano))
		if err != nil {
			return index.failure(fmt.Errorf("index message text: %w", err))
		}
		id, err := inserted.LastInsertId()
		if err != nil {
			return fmt.Errorf("identify indexed message text: %w", err)
		}
		if _, err := fullText.ExecContext(ctx, id, fold(part.Text)); err != nil {
			return index.failure(fmt.Errorf("index full-text message: %w", err))
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
	file := indexedFile{id: previous.id, resumeID: previous.resumeID, harness: string(source.Harness), path: path, stamp: result.stamp, checkpoint: string(checkpoint), issues: string(encoded), omitted: omitted}
	if _, err = index.tx.ExecContext(ctx, "UPDATE files SET stamp=?,checkpoint=?,issues=?,omitted=? WHERE id=?", file.stamp, file.checkpoint, file.issues, file.omitted, file.id); err != nil {
		return file, index.failure(fmt.Errorf("save history checkpoint: %w", err))
	}
	return file, nil
}

func newIndexWriter() *indexWriter {
	var conversation Conversation
	checkpoint := indexCheckpoint{Conversation: conversation, Identity: "", Extra: "", Projection: "", Digest: "", Size: 0, Lines: 0, Recognized: false, Complete: false}
	return &indexWriter{conversations: nil, open: false, checkpoint: checkpoint, resume: nil, hash: nil, reason: ""}
}

func (writer *indexWriter) checkSnapshot(file *os.File, info os.FileInfo) error {
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("check indexed transcript: %w", err)
	}
	if stampFile(after) != stampFile(info) {
		writer.checkpoint.Complete = false
	}
	return nil
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
	if part.Role == "tool" {
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

func (checkpoint indexCheckpoint) resumable(writer *indexWriter, info os.FileInfo) bool {
	return checkpoint.Complete && checkpoint.Size < info.Size() &&
		checkpoint.Identity == writer.checkpoint.Identity && checkpoint.Extra == writer.checkpoint.Extra
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
	if !checkpoint.resumable(writer, info) {
		if !checkpoint.Complete {
			writer.reason = "incomplete checkpoint"
		}
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
	writer.reason = "rewritten prefix"
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
