package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
)

func (s *FileStore) load(ctx context.Context) (snapshot, error) {
	if _, err := os.Stat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	}

	lock, err := openStoreLock(ctx, s.path+".lock", nil)
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

	lock, err := openExistingStoreLock(ctx, s.path+".lock")
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

	lock, err := openStoreLock(ctx, s.path+".lock", nil)
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

	entries, err := readJournal(s.path)
	if err != nil {
		return err
	}

	if baseline.JournalSequence == s.snapshot.JournalSequence &&
		(len(entries) == 0 || entries[len(entries)-1].Sequence <= baseline.JournalSequence) {
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
		if err := persistJournalSnapshot(s.path, candidate); err != nil {
			return err
		}
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
// advance the public revision and schedule a prompt snapshot write.
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

func (s *MemoryStore) command(ctx context.Context, entry journalEntry) (journalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.owner == nil {
		return journalResult{}, ErrStoreClosed
	}

	lock, err := openStoreLock(ctx, s.path+".lock", nil)
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

		return result, nil
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
		if err := appendJournal(s.path, entry); err != nil {
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
	}

	s.acceptSnapshotLocked(candidate, result.changes)

	return result, nil
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
		if entry.Observations[index].Kind() == "report" {
			return true
		}
	}

	return false
}

func (s *MemoryStore) journalBaseline() (snapshot, error) {
	baseline := s.snapshot

	data, err := readSnapshotFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return snapshot{}, err
	}

	var header struct {
		Sequence uint64 `json:"journal_sequence"`
	}
	if json.Unmarshal(data, &header) == nil && header.Sequence > baseline.JournalSequence {
		// Only reset can publish a checkpoint ahead of the live owner's memory.
		baseline, err = NewFileStore(s.path, s.reducer.rules).loadSnapshot()
		if err != nil {
			return snapshot{}, err
		}
	}

	return baseline, nil
}
