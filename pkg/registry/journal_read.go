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
)

func (s *FileStore) load(ctx context.Context) (snapshot, error) {
	if _, err := os.Stat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
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
	if _, err := os.Stat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
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

func (s *MemoryStore) drainLocked(ctx context.Context) error {
	if s.owner == nil {
		return ErrStoreClosed
	}

	lock, err := openStoreLock(ctx, storeLockPath(s.path), nil)
	if err != nil {
		return err
	}

	err = closeStoreLock(lock, s.consumeJournalLocked(ctx))
	if err == nil {
		s.refreshDetailsLocked()
	}

	return err
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
	path := journalPath(s.path)

	info, err := registryFSFor(path).Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		s.journal = journalPosition{info: nil, offset: 0}
		return nil
	}

	if err != nil {
		return fmt.Errorf("stating journal: %w", err)
	}

	s.journal = journalPosition{info: info, offset: info.Size()}

	return nil
}

func (s *MemoryStore) checkpointLocked(snap snapshot) error {
	// A checkpoint that fails midway leaves a journal of unknown content.
	s.journal = journalPosition{info: nil, offset: 0}

	if err := persistJournalSnapshot(s.path, snap); err != nil {
		return err
	}

	return s.journalWrittenLocked()
}

func (s *MemoryStore) command(ctx context.Context, entry journalEntry) (journalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.owner == nil {
		return journalResult{}, ErrStoreClosed
	}

	lock, err := openStoreLock(ctx, storeLockPath(s.path), nil)
	if err != nil {
		return journalResult{}, err
	}

	result, err := s.commandLocked(ctx, entry)

	return result, closeStoreLock(lock, err)
}

func (s *MemoryStore) commandLocked(
	ctx context.Context,
	entry journalEntry,
) (journalResult, error) {
	if entry.Reset {
		return s.resetLocked(ctx)
	}

	if err := s.consumeJournalLocked(ctx); err != nil {
		return journalResult{}, err
	}

	candidate := cloneRegistrySnapshotForMutation(s.snapshot)
	entry.Sequence = candidate.JournalSequence + 1
	entry.ReceivedAt = s.now().UTC()

	result, err := s.reducer.applyJournalEntry(ctx, &candidate, entry)
	if err != nil {
		return journalResult{}, err
	}

	if err := validateSnapshot(candidate); err != nil {
		return journalResult{}, err
	}

	if err := validateNativePayloadChanges(s.snapshot, candidate); err != nil {
		return journalResult{}, err
	}

	if err := ctx.Err(); err != nil {
		return journalResult{}, fmt.Errorf("committing registry command: %w", err)
	}

	if journalWorthy(entry, result) ||
		!maps.Equal(candidate.EndedProcesses, s.snapshot.EndedProcesses) {
		if err := s.appendJournalLocked(entry); err != nil {
			return journalResult{}, err
		}
	} else {
		// Evidence-only refreshes are re-derived after a restart. Keeping the
		// sequence unchanged leaves the next journaled entry contiguous for
		// fallback writers that fold the on-disk snapshot and journal.
		candidate.JournalSequence = s.snapshot.JournalSequence
	}

	if expireTombstones(&candidate, entry.ReceivedAt, s.tombstoneTTL) > 0 {
		result.changes = stateChanges(
			State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt},
			State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt},
		)
		s.acceptUnjournaledLocked(candidate, result.changes)

		return result, nil
	}

	s.acceptSnapshotLocked(candidate, result.changes)

	return result, nil
}

func (s *MemoryStore) resetLocked(ctx context.Context) (journalResult, error) {
	candidate, result, err := resetJournalSnapshot(
		ctx,
		s.path,
		s.now().UTC(),
		s.snapshot.JournalSequence,
		len(s.snapshot.Sessions),
	)
	if err != nil {
		return journalResult{}, err
	}

	result.changes = stateChanges(
		State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt},
		State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt},
	)
	s.acceptSnapshotLocked(candidate, result.changes)
	s.journal = journalPosition{info: nil, offset: 0}

	return result, nil
}

func (s *MemoryStore) appendJournalLocked(entry journalEntry) error {
	if err := appendJournal(s.path, entry); err != nil {
		return err
	}

	return s.journalWrittenLocked()
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
