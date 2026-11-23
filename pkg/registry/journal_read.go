package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"time"
)

type pendingCommand struct {
	entry journalEntry
	//exhaustruct:optional
	result journalResult
	//exhaustruct:optional
	candidate snapshot
	//exhaustruct:optional
	journaled bool
	// unjournaled marks changes that replaying the journal does not reproduce.
	//exhaustruct:optional
	unjournaled bool
	//exhaustruct:optional
	cleared int
	//exhaustruct:optional
	baseRevision uint64
}

func (s *FileStore) load(ctx context.Context) (snapshot, error) {
	if _, err := registryFSFor(s.path).Lstat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	}

	lock, err := openStoreLock(ctx, storeLockPath(s.path), nil)
	if err != nil {
		return snapshot{}, err
	}

	return s.loadLocked(ctx, lock)
}

// Verify reads and validates the snapshot and journal as List does, without
// creating the state directory or the lock file, and returns the number of
// stored sessions. It takes the store lock only when the lock file exists;
// writers create that file before their first write.
func (s *FileStore) Verify(ctx context.Context) (int, error) {
	if _, err := registryFSFor(s.path).Lstat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	lock, err := openExistingStoreLock(ctx, storeLockPath(s.path))
	if err != nil {
		return 0, err
	}

	snap, err := s.loadLocked(ctx, lock)
	if err != nil {
		return 0, err
	}

	return len(snap.Sessions), nil
}

func (s *FileStore) loadLocked(ctx context.Context, lock *storeLock) (snapshot, error) {
	snap, err := s.loadSnapshot()
	if err != nil {
		return snapshot{}, closeStoreLock(lock, err)
	}

	entries, err := readJournal(s.path)
	if err != nil {
		return snapshot{}, closeStoreLock(lock, err)
	}

	if err := s.reducer.foldJournal(ctx, &snap, entries); err != nil {
		return snapshot{}, closeStoreLock(lock, err)
	}

	return snap, closeStoreLock(lock, validateSnapshot(snap))
}

func (s *MemoryStore) lockCurrent(ctx context.Context) (func(), error) {
	s.mu.RLock()

	if s.owner != nil && s.filesUnchangedLocked() && s.detailsCurrentLocked() {
		return s.mu.RUnlock, nil
	}

	s.mu.RUnlock()

	if err := s.lockCurrentExclusive(ctx); err != nil {
		return nil, err
	}

	return s.mu.Unlock, nil
}

func (s *MemoryStore) lockCurrentExclusive(ctx context.Context) error {
	for {
		s.mu.Lock()
		if s.owner == nil {
			s.mu.Unlock()
			return ErrStoreClosed
		}

		if s.filesUnchangedLocked() {
			s.refreshDetailsLocked()
			return nil
		}

		// TryLock never waits, so trying it under s.mu keeps the lock order.
		if s.filesMu.TryLock() {
			break
		}

		changed := s.filesChanged
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for registry files: %w", ctx.Err())
		case <-changed:
		}
	}

	s.mu.Unlock()

	lock, err := openStoreLock(ctx, storeLockPath(s.path), nil)
	if err != nil {
		s.releaseFiles()
		return err
	}

	s.mu.Lock()

	err = s.unlockFilesLocked(lock, s.drainLocked(ctx))
	if err != nil {
		s.mu.Unlock()
	}

	return err
}

func (s *MemoryStore) drainLocked(ctx context.Context) error {
	if s.owner == nil {
		return ErrStoreClosed
	}

	err := s.consumeJournalLocked(ctx)
	if err == nil {
		s.refreshDetailsLocked()
	}

	return err
}

func (s *MemoryStore) lockFiles(ctx context.Context) (*storeLock, error) {
	// The mutex lets this process's goroutines hand off without polling the
	// flock. Both come before s.mu, which is never held across file syncs.
	s.filesMu.Lock()

	lock, err := openStoreLock(ctx, storeLockPath(s.path), nil)
	if err != nil {
		s.releaseFiles()
		return nil, err
	}

	if s.filesLocked != nil {
		s.filesLocked()
	}

	return lock, nil
}

func (s *MemoryStore) releaseFiles() {
	s.filesMu.Unlock()

	s.mu.Lock()
	s.signalFilesLocked()
	s.mu.Unlock()
}

func (s *MemoryStore) unlockFilesLocked(lock *storeLock, err error) error {
	s.filesDrained = false
	err = closeStoreLock(lock, err)
	s.filesMu.Unlock()
	s.signalFilesLocked()

	return err
}

func (s *MemoryStore) signalFilesLocked() {
	close(s.filesChanged)
	s.filesChanged = make(chan struct{})
}

func (s *MemoryStore) filesUnchangedLocked() bool {
	// A goroutine of this process holds the flock and drained the files, so no
	// other process can have written since.
	if s.filesDrained {
		return true
	}

	journal := journalPath(s.path)

	info, err := registryFSFor(journal).Lstat(journal)
	if !unchangedFile(s.journal.info, info, err) {
		return false
	}

	info, err = registryFSFor(s.path).Lstat(s.path)

	return unchangedFile(s.checkpoint.info, info, err)
}

func unchangedFile(before os.FileInfo, after os.FileInfo, err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return before == nil
	}

	// Writers append to the journal or rename a new file into place, so an
	// unchanged regular file holds the bytes already read from it.
	return err == nil && before != nil && after.Mode().IsRegular() &&
		os.SameFile(before, after) && before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}

func (s *MemoryStore) consumeJournalLocked(ctx context.Context) error {
	baseline, err := s.journalBaseline()
	if err != nil {
		return err
	}

	// A checkpoint ahead of memory replaces what this owner applied so far.
	entries, position, err := s.unreadJournal(
		baseline.JournalSequence != s.snapshot.JournalSequence,
	)
	if err != nil {
		return err
	}

	if baseline.JournalSequence == s.snapshot.JournalSequence &&
		(len(entries) == 0 || entries[len(entries)-1].Sequence <= baseline.JournalSequence) {
		s.journal = position
		s.filesDrained = true
		s.signalFilesLocked()

		return nil
	}

	candidate, rejected, err := s.reducer.replayJournal(ctx, baseline, entries)
	if err != nil {
		return fmt.Errorf("reducing journal: %w", err)
	}

	if err := validateSnapshot(candidate); err != nil {
		return err
	}

	if len(rejected) > 0 {
		// Checkpoint the rejection's memory-only evidence before discarding its
		// journal entry; a restart must not accept what this owner rejected.
		if err := s.checkpointLocked(candidate); err != nil {
			return err
		}
	} else {
		s.journal = position
	}

	changes := stateChanges(
		State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt},
		State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt},
	)
	s.acceptSnapshotLocked(candidate, changes)

	for _, err := range rejected {
		slog.Warn("dropping rejected registry observation", "path", s.path, "error", err)
	}

	s.filesDrained = true
	s.signalFilesLocked()

	return nil
}

// acceptSnapshotLocked installs candidate. Only consumer-visible changes
// advance the public revision and schedule a snapshot write.
func (s *MemoryStore) acceptSnapshotLocked(candidate snapshot, changes []Change) {
	s.snapshot = candidate
	s.storageRevision++

	if len(changes) == 0 {
		return
	}

	s.visibleRevision = s.storageRevision
	s.revision++
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
	s.signalDirty()
}

func (s *MemoryStore) acceptUnjournaledLocked(candidate snapshot, changes []Change) {
	s.acceptSnapshotLocked(candidate, changes)

	if len(changes) > 0 {
		// Replaying the journal does not reproduce these changes.
		s.unjournaledRevision = s.storageRevision
	}
}

func (s *MemoryStore) unreadJournal(whole bool) ([]decodedJournalEntry, journalPosition, error) {
	path := journalPath(s.path)

	info, err := registryFSFor(path).Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, journalPosition{info: nil, offset: 0}, nil
	}

	if err != nil {
		return nil, journalPosition{}, fmt.Errorf("stating journal: %w", err)
	}

	position := journalPosition{info: info, offset: s.journal.offset}
	// Appends keep the bytes before them; any other change starts a new journal.
	if whole || s.journal.info == nil || !os.SameFile(s.journal.info, info) ||
		info.Size() < position.offset {
		position.offset = 0
	}

	if info.Size() == position.offset {
		return nil, position, nil
	}

	data, err := readJournalFrom(path, position.offset)
	if err != nil {
		return nil, journalPosition{}, err
	}

	entries, length, err := decodeJournal(data)
	position.offset += int64(length)

	return entries, position, err
}

func readJournalFrom(path string, offset int64) ([]byte, error) {
	file, err := registryFSFor(path).Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading journal: %w", err)
	}
	// Closing a read-only journal has no pending writes to finalize.
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading journal: %w", err)
	}

	if info.Size()-offset > maxSnapshotBytes {
		return nil, fmt.Errorf("reading journal: %w", ErrStoreTooLarge)
	}

	data := make([]byte, max(info.Size()-offset, 0))

	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading journal: %w", err)
	}

	return data[:n], nil
}

// journalWrittenLocked records that the whole journal is applied, which holds
// after this owner appends to it or replaces it under the store lock.
func (s *MemoryStore) journalWrittenLocked() error {
	position, err := writtenJournalPosition(s.path)
	if err != nil {
		return err
	}

	s.journal = position

	return nil
}

func writtenJournalPosition(path string) (journalPosition, error) {
	path = journalPath(path)

	info, err := registryFSFor(path).Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return journalPosition{info: nil, offset: 0}, nil
	}

	if err != nil {
		return journalPosition{}, fmt.Errorf("stating journal: %w", err)
	}

	return journalPosition{info: info, offset: info.Size()}, nil
}

func (s *MemoryStore) checkpointLocked(snap snapshot) error {
	data, err := encodeSnapshot(snap)
	if err != nil {
		return err
	}

	// A checkpoint that fails midway leaves a journal of unknown content.
	s.journal = journalPosition{info: nil, offset: 0}

	if err := persistJournalSnapshot(s.path, data); err != nil {
		return err
	}

	return s.checkpointWrittenLocked(snap.JournalSequence)
}

func (s *MemoryStore) checkpointWrittenLocked(sequence uint64) error {
	info, err := registryFSFor(s.path).Lstat(s.path)
	if err != nil {
		return fmt.Errorf("stating store: %w", err)
	}

	s.checkpoint = checkpointFile{info: info, sequence: sequence}

	return s.journalWrittenLocked()
}

func (s *MemoryStore) command(ctx context.Context, entry journalEntry) (journalResult, error) {
	lock, err := s.lockFiles(ctx)
	if err != nil {
		return journalResult{}, err
	}

	s.mu.Lock()

	pending, err := s.prepareCommandLocked(ctx, entry)
	s.mu.Unlock()

	// Readers see the state before this command until its files are synced.
	var position journalPosition
	if err == nil {
		position, err = s.writeCommand(ctx, &pending)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil {
		err = s.commitCommandLocked(&pending, position)
	}

	return pending.result, s.unlockFilesLocked(lock, err)
}

func (s *MemoryStore) prepareCommandLocked(
	ctx context.Context,
	entry journalEntry,
) (pendingCommand, error) {
	pending := pendingCommand{entry: entry}

	if s.owner == nil {
		return pending, ErrStoreClosed
	}

	// Reset must still clear a journal that does not apply, counting what
	// memory holds instead.
	if err := s.consumeJournalLocked(ctx); err != nil && !entry.Reset {
		return pending, err
	}

	pending.entry.ReceivedAt = s.now().UTC()
	pending.baseRevision = s.storageRevision

	if entry.Reset {
		pending.entry.Sequence = s.snapshot.JournalSequence
		pending.cleared = len(s.snapshot.Sessions)

		return pending, nil
	}

	err := s.reduceCommandLocked(ctx, &pending)

	return pending, err
}

func (s *MemoryStore) reduceCommandLocked(ctx context.Context, pending *pendingCommand) error {
	candidate := cloneRegistrySnapshotForMutation(s.snapshot)
	pending.entry.Sequence = candidate.JournalSequence + 1

	result, err := s.reducer.applyJournalEntry(ctx, &candidate, pending.entry)
	if err != nil {
		return err
	}

	if err := validateSnapshot(candidate); err != nil {
		return err
	}

	if err := validateNativePayloadChanges(s.snapshot, candidate); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("committing registry command: %w", err)
	}

	pending.journaled = journalWorthy(pending.entry, result) ||
		!maps.Equal(candidate.EndedProcesses, s.snapshot.EndedProcesses)
	if !pending.journaled {
		// Evidence-only refreshes are re-derived after a restart. Keeping the
		// sequence unchanged leaves the next journaled entry contiguous for
		// fallback writers that fold the on-disk snapshot and journal.
		candidate.JournalSequence = s.snapshot.JournalSequence
	}

	if expireTombstones(&candidate, pending.entry.ReceivedAt, s.tombstoneTTL) > 0 {
		result.changes = stateChanges(
			State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt},
			State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt},
		)
		pending.unjournaled = true
	}

	pending.result = result
	pending.candidate = candidate

	return nil
}

func (s *MemoryStore) writeCommand(
	ctx context.Context,
	pending *pendingCommand,
) (journalPosition, error) {
	if pending.entry.Reset {
		candidate, result, err := resetJournalSnapshot(
			ctx,
			s.path,
			pending.entry.ReceivedAt,
			pending.entry.Sequence,
			pending.cleared,
		)
		pending.candidate = candidate
		pending.result = result

		return journalPosition{info: nil, offset: 0}, err
	}

	if !pending.journaled {
		return journalPosition{info: nil, offset: 0}, nil
	}

	written, err := writeJournalEntry(s.path, pending.entry)
	if err != nil {
		return journalPosition{}, err
	}

	position, err := writtenJournalPosition(s.path)
	if err != nil {
		return journalPosition{}, errors.Join(err, written.close())
	}

	return position, written.sync()
}

func (s *MemoryStore) commitCommandLocked(pending *pendingCommand, position journalPosition) error {
	previous := State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt}

	switch {
	case pending.entry.Reset:
		pending.result.changes = stateChanges(
			previous,
			State{Sessions: pending.candidate.Sessions, UpdatedAt: pending.candidate.UpdatedAt},
		)
		s.acceptSnapshotLocked(pending.candidate, pending.result.changes)
		// The reset wrote a checkpoint of the candidate, so nothing is left to persist.
		s.persistedRevision = s.storageRevision
		s.persistedAt = time.Now()

		return s.checkpointWrittenLocked(pending.candidate.JournalSequence)
	case pending.journaled:
		s.journal = position
	}

	refreshed := s.storageRevision != pending.baseRevision
	if pending.unjournaled {
		s.acceptUnjournaledLocked(pending.candidate, pending.result.changes)
	} else {
		s.acceptSnapshotLocked(pending.candidate, pending.result.changes)
	}

	// Readers may have refreshed expired details while the files synced; the
	// candidate predates that refresh.
	if refreshed {
		s.refreshDetailsLocked()
	}

	return nil
}

// journalWorthy reports whether losing entry in an owner crash would matter:
// consumer-visible changes, native reports, and administrative commands.
// Heartbeat-only process, placement, listing, and screen refreshes are not,
// unless they record an ended process that later reports are checked against.
func journalWorthy(entry journalEntry, result journalResult) bool {
	if entry.Reset || entry.DeleteAfter != nil || len(result.changes) > 0 {
		return true
	}

	for index := range entry.Observations {
		if entry.Observations[index].Kind() == EvidenceReport {
			return true
		}
	}

	return false
}

func (s *MemoryStore) journalBaseline() (snapshot, error) {
	baseline := s.snapshot

	sequence, err := s.checkpointSequence()
	if err != nil {
		return snapshot{}, err
	}

	if sequence > baseline.JournalSequence {
		// Only reset can publish a checkpoint ahead of the live owner's memory.
		baseline, err = NewFileStore(s.path, s.reducer.rules).loadSnapshot()
		if err != nil {
			return snapshot{}, err
		}
	}

	return baseline, nil
}

func (s *MemoryStore) checkpointSequence() (uint64, error) {
	info, statErr := registryFSFor(s.path).Lstat(s.path)
	if statErr == nil && s.checkpoint.describes(info) {
		return s.checkpoint.sequence, nil
	}

	data, err := readSnapshotFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}

	var header struct {
		Sequence uint64 `json:"journal_sequence"`
	}
	if json.Unmarshal(data, &header) != nil {
		header.Sequence = 0
	}

	s.checkpoint = checkpointFile{info: nil, sequence: 0}
	if statErr == nil && err == nil {
		s.checkpoint = checkpointFile{info: info, sequence: header.Sequence}
	}

	return header.Sequence, nil
}

func (c checkpointFile) describes(info os.FileInfo) bool {
	// Every checkpoint is a new file renamed into place, so an unchanged
	// regular file still holds the sequence read from it.
	return c.info != nil && info.Mode().IsRegular() && os.SameFile(c.info, info) &&
		c.info.Size() == info.Size() && c.info.ModTime().Equal(info.ModTime())
}
