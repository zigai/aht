package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"
)

const maxJournalBytes = 64 << 20

var (
	ErrJournalTooLarge = errors.New("registry journal exceeds size limit")
	ErrStoreOwned      = errors.New("registry already has a live owner")
	ErrStoreClosed     = errors.New("registry owner is closed")
)

type Journal struct{ *FileStore }

type journalEntry struct {
	Sequence     uint64         `json:"sequence"`
	ReceivedAt   time.Time      `json:"received_at"`
	Observations []Observation  `json:"observations,omitempty"`
	DeleteAfter  *time.Duration `json:"delete_after,omitempty"`
	Reset        bool           `json:"reset,omitempty"`
}

type journalResult struct {
	changes  []Change
	sessions []Session
	gc       GCResult
	reset    ResetResult
}

func NewJournal(path string, rules Rules) *Journal {
	return &Journal{FileStore: NewFileStore(path, rules)}
}

func (s *Journal) Observe(ctx context.Context, observation Observation) (Session, error) {
	sessions, err := s.Append(ctx, []Observation{observation})
	if err != nil {
		return Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	snap, err := s.loadContext(ctx)
	if err != nil {
		return Session{}, err
	}
	id := findMatchingSession(snap.Sessions, observation)
	if id == "" {
		id = sessionIDForObservation(observation)
	}
	return s.Get(ctx, id)
}

func (s *Journal) ObserveBatch(ctx context.Context, observations []Observation) ([]Session, error) {
	return s.Append(ctx, observations)
}

func (s *Journal) Append(ctx context.Context, observations []Observation) ([]Session, error) {
	result, err := s.transact(ctx, journalEntry{Sequence: 0, ReceivedAt: s.now().UTC(), Observations: observations, DeleteAfter: nil, Reset: false})
	return result.sessions, err
}

func (s *Journal) GC(ctx context.Context, deleteAfter time.Duration) (GCResult, error) {
	result, err := s.transact(ctx, journalEntry{Sequence: 0, ReceivedAt: s.now().UTC(), Observations: nil, DeleteAfter: &deleteAfter, Reset: false})
	return result.gc, err
}

func (s *Journal) Reset(ctx context.Context) (ResetResult, error) {
	result, err := s.transact(ctx, journalEntry{Sequence: 0, ReceivedAt: s.now().UTC(), Observations: nil, DeleteAfter: nil, Reset: true})
	return result.reset, err
}

func (s *Journal) transact(ctx context.Context, entry journalEntry) (journalResult, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return journalResult{}, fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := openStoreLock(ctx, s.path+".lock", s.onLockContention)
	if err != nil {
		return journalResult{}, err
	}
	result, err := s.transactLocked(ctx, entry)
	return result, closeStoreLock(lock, err)
}

func (s *Journal) transactLocked(ctx context.Context, entry journalEntry) (journalResult, error) {
	snap, err := s.loadSnapshot()
	if err != nil {
		if !entry.Reset {
			return journalResult{}, err
		}
		snap = newSnapshot()
	}
	entries, err := readJournal(s.path)
	if err != nil {
		return journalResult{}, err
	}
	if err := s.reducer.foldJournal(ctx, &snap, entries); err != nil {
		return journalResult{}, err
	}
	entry.Sequence = snap.JournalSequence + 1
	result, err := s.reducer.applyJournalEntry(ctx, &snap, entry)
	if err != nil {
		return journalResult{}, err
	}
	if err := validateSnapshot(snap); err != nil {
		return journalResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return journalResult{}, fmt.Errorf("updating registry: %w", err)
	}
	owner, err := tryStoreLock(s.path + ".owner.lock")
	if err != nil && !errors.Is(err, ErrStoreOwned) {
		return journalResult{}, err
	}
	if owner != nil {
		return result, closeStoreLock(owner, persistJournalSnapshot(s.path, snap))
	}
	if err := appendJournal(s.path, entry); err != nil {
		return journalResult{}, err
	}
	return result, nil
}

func (r Reducer) applyJournalEntry(ctx context.Context, snap *snapshot, entry journalEntry) (journalResult, error) {
	var result journalResult
	before := State{Sessions: maps.Clone(snap.Sessions), UpdatedAt: snap.UpdatedAt}
	switch {
	case entry.Reset:
		result.reset = ResetResult{Cleared: len(snap.Sessions), Remaining: 0}
		*snap = newSnapshot()
		snap.UpdatedAt = entry.ReceivedAt
	case entry.DeleteAfter != nil:
		result.gc.Deleted = deleteExpiredGoneSessions(snap.Sessions, entry.ReceivedAt, *entry.DeleteAfter, func(session Session) time.Time { return session.PresenceChangedAt })
		result.gc.Remaining = len(snap.Sessions)
		snap.UpdatedAt = maxTime(snap.UpdatedAt, entry.ReceivedAt)
	default:
		saved, err := r.applyObservationBatch(ctx, snap, entry.Observations, entry.ReceivedAt)
		if err != nil {
			return journalResult{}, err
		}
		result.sessions = saved
	}
	snap.JournalSequence = entry.Sequence
	result.changes = stateChanges(before, State{Sessions: snap.Sessions, UpdatedAt: snap.UpdatedAt})
	return result, nil
}

func (r Reducer) foldJournal(ctx context.Context, snap *snapshot, entries []journalEntry) error {
	for _, entry := range entries {
		if entry.Sequence <= snap.JournalSequence {
			continue
		}
		if _, err := r.applyJournalEntry(ctx, snap, entry); err != nil {
			return err
		}
	}
	return nil
}

func readJournal(path string) ([]journalEntry, error) {
	data, err := readSnapshotFile(path + ".journal.jsonl")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading journal: %w", err)
	}
	var entries []journalEntry
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var entry journalEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("decoding journal: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func appendJournal(path string, entry journalEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encoding journal: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path+".journal.jsonl", os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}
	if err := errors.Join(writeJournal(file, data), file.Close()); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func writeJournal(file *os.File, data []byte) error {
	offset, err := journalAppendOffset(file)
	if err != nil {
		return err
	}
	if offset+int64(len(data)) > maxJournalBytes {
		return ErrJournalTooLarge
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(fmt.Errorf("appending journal: %w", err), file.Truncate(offset), file.Sync())
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("syncing journal: %w", err)
	}
	return nil
}

func persistJournalSnapshot(path string, snap snapshot) error {
	if err := writeSnapshotAtomic(path, snap); err != nil {
		return err
	}
	file, err := os.OpenFile(path+".journal.jsonl", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("truncating journal: %w", err)
	}
	return errors.Join(file.Sync(), file.Close())
}

func journalAppendOffset(file *os.File) (int64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stating journal: %w", err)
	}
	if info.Size() == 0 {
		return 0, nil
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
		return 0, fmt.Errorf("reading journal tail: %w", err)
	}
	if last[0] == '\n' {
		return info.Size(), nil
	}
	data, err := readSnapshotFile(file.Name())
	if err != nil {
		return 0, err
	}
	offset := int64(bytes.LastIndexByte(data, '\n') + 1)
	if err := file.Truncate(offset); err != nil {
		return 0, fmt.Errorf("discarding incomplete journal tail: %w", err)
	}
	return offset, nil
}

func (r Reducer) replayJournal(ctx context.Context, previous snapshot, entries []journalEntry) (snapshot, []Change, error) {
	candidate := cloneRegistrySnapshot(previous)
	if err := r.foldJournal(ctx, &candidate, entries); err != nil {
		return snapshot{}, nil, err
	}
	return candidate, stateChanges(State{Sessions: previous.Sessions, UpdatedAt: previous.UpdatedAt}, State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt}), nil
}
