package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *FileStore) load() (snapshot, error) { return s.loadContext(context.Background()) }

func (s *FileStore) loadContext(ctx context.Context) (snapshot, error) {
	if _, err := os.Stat(filepath.Dir(s.path)); errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	}
	lock, err := openStoreLock(ctx, s.path+".lock", nil)
	if err != nil {
		return snapshot{}, err
	}
	snap, err := s.loadSnapshot()
	if err != nil {
		return snapshot{}, closeStoreLock(lock, err)
	}
	entries, err := readJournal(s.path)
	if err != nil {
		return snapshot{}, closeStoreLock(lock, err)
	}
	err = s.reducer.foldJournal(ctx, &snap, entries)
	return snap, closeStoreLock(lock, err)
}

func (s *MemoryStore) drainLocked(ctx context.Context) error {
	if s.owner == nil {
		return ErrStoreClosed
	}
	lock, err := openStoreLock(ctx, s.path+".lock", nil)
	if err != nil {
		return err
	}
	return closeStoreLock(lock, s.consumeJournalLocked(ctx))
}

func (s *MemoryStore) consumeJournalLocked(ctx context.Context) error {
	entries, err := readJournal(s.path)
	if err != nil {
		return err
	}
	if len(entries) == 0 || entries[len(entries)-1].Sequence <= s.snapshot.JournalSequence {
		return nil
	}
	candidate, changes, err := s.reducer.replayJournal(ctx, s.snapshot, entries)
	if err != nil {
		return fmt.Errorf("reducing journal: %w", err)
	}
	if err := validateSnapshot(candidate); err != nil {
		return err
	}
	s.acceptSnapshotLocked(candidate, changes)
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

func (s *MemoryStore) commandLocked(ctx context.Context, entry journalEntry) (journalResult, error) {
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
	if journalWorthy(entry, result) {
		if err := appendJournal(s.path, entry); err != nil {
			return journalResult{}, err
		}
	} else {
		// Evidence-only refreshes are re-derived after a restart. Keeping the
		// sequence unchanged leaves the next journaled entry contiguous for
		// fallback writers that fold the on-disk snapshot and journal.
		candidate.JournalSequence = s.snapshot.JournalSequence
	}
	if expireTombstones(candidate.Sessions, entry.ReceivedAt, s.tombstoneTTL) > 0 {
		result.changes = stateChanges(State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt}, State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt})
	}
	s.acceptSnapshotLocked(candidate, result.changes)
	return result, nil
}

// journalWorthy reports whether losing entry in an owner crash would matter:
// consumer-visible changes, native reports, and administrative commands.
// Heartbeat-only process, placement, listing, and screen refreshes are not.
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
