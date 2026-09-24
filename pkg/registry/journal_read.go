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
	s.signalDirty()
	return nil
}

func (s *MemoryStore) acceptSnapshotLocked(candidate snapshot, changes []Change) {
	s.snapshot = candidate
	s.storageRevision++
	if len(changes) > 0 {
		s.revision++
		close(s.stateChanged)
		s.stateChanged = make(chan struct{})
	}
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
	if err := appendJournal(s.path, entry); err != nil {
		return journalResult{}, err
	}
	s.acceptSnapshotLocked(candidate, result.changes)
	s.signalDirty()
	return result, nil
}
