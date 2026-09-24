package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	storeSchemaVersion      = 3
	maxObservedAtFutureSkew = 5 * time.Minute
	maxSnapshotBytes        = 64 << 20

	// IntegrationActivityLease is the maximum age of a matching integration
	// transition before multiplexer screen evidence becomes authoritative again.
	IntegrationActivityLease = 30 * time.Second
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrHarnessRequired     = errors.New("harness is required")
	ErrObservationIdentity = errors.New("observation requires identity")
	ErrObservationConflict = errors.New("observation conflicts with accepted evidence")
	ErrCorruptStore        = errors.New("corrupt registry store")
	ErrStoreTooLarge       = errors.New("registry store exceeds size limit")

	_ Store = (*Journal)(nil)
)

type UnsupportedSchemaError struct {
	Path    string
	Version int
}

type snapshot struct {
	JournalSequence uint64             `json:"journal_sequence,omitempty"`
	SchemaVersion   int                `json:"schema_version"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Sessions        map[string]Session `json:"sessions"`
}

type GCResult struct {
	Deleted   int `json:"deleted"`
	Remaining int `json:"remaining"`
}

type ResetResult struct {
	Cleared   int `json:"cleared"`
	Remaining int `json:"remaining"`
}

type FileStore struct {
	reducer          Reducer
	path             string
	now              func() time.Time
	onLockContention func()
}

func (e *UnsupportedSchemaError) Error() string {
	version := "missing"
	if e.Version != 0 {
		version = strconv.Itoa(e.Version)
	}
	return fmt.Sprintf("unsupported store schema %s at %s; run aht --store %s manage state reset --force or move/remove the file", version, e.Path, e.Path)
}

func NewFileStore(path string, rules Rules) *FileStore {
	if path == "" {
		path = DefaultStorePath()
	}
	return &FileStore{reducer: NewReducer(rules), path: path, now: func() time.Time { return time.Now().UTC() }, onLockContention: nil}
}

func (s *FileStore) Path() string { return s.path }

func (s *FileStore) List(ctx context.Context, filter Filter) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}
	sessions, _, err := s.watchSnapshot(ctx, filter)
	return sessions, err
}

func (s *FileStore) Get(ctx context.Context, id string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, fmt.Errorf("checking context: %w", err)
	}
	snap, err := s.loadContext(ctx)
	if err != nil {
		return Session{}, err
	}
	session, ok := snap.Sessions[id]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	session.SchemaVersion = storeSchemaVersion
	return session, nil
}

func (s *FileStore) SummaryWithOptions(ctx context.Context, filter Filter, opts SummaryOptions) ([]Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}
	if opts.GroupBy != "" && !opts.GroupBy.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedGroupBy, opts.GroupBy)
	}
	sessions, _, err := s.watchSnapshot(ctx, filter)
	if err != nil {
		return nil, err
	}
	return SummariesWithOptions(sessions, opts), nil
}

func (s *FileStore) setNowForTest(now func() time.Time)   { s.now = now }
func (s *FileStore) setOnLockContentionForTest(fn func()) { s.onLockContention = fn }

func (s *FileStore) withSnapshot(ctx context.Context, mutator func(*snapshot) error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	lock, err := openStoreLock(ctx, s.path+".lock", s.onLockContention)
	if err != nil {
		return err
	}
	snap, err := s.loadSnapshot()
	if err != nil {
		return closeStoreLock(lock, err)
	}
	if err := mutator(&snap); err != nil {
		return closeStoreLock(lock, err)
	}
	if err := validateSnapshot(snap); err != nil {
		return closeStoreLock(lock, fmt.Errorf("validating updated store: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return closeStoreLock(lock, fmt.Errorf("updating store: %w", err))
	}
	return closeStoreLock(lock, writeSnapshotAtomic(s.path, snap))
}

func closeStoreLock(lock *storeLock, err error) error {
	if closeErr := lock.Close(); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	return err
}

func (s *FileStore) loadSnapshot() (snapshot, error) {
	data, err := readSnapshotFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newSnapshot(), nil
		}
		return snapshot{}, fmt.Errorf("reading store: %w", err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return snapshot{}, fmt.Errorf("decoding snapshot version: %w", err)
	}
	if header.SchemaVersion != storeSchemaVersion {
		return snapshot{}, &UnsupportedSchemaError{Path: s.path, Version: header.SchemaVersion}
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return snapshot{}, fmt.Errorf("parsing store %s: %w", s.path, err)
	}
	if snap.SchemaVersion != storeSchemaVersion {
		return snapshot{}, &UnsupportedSchemaError{Path: s.path, Version: snap.SchemaVersion}
	}
	if snap.Sessions == nil {
		snap.Sessions = make(map[string]Session)
	}
	if err := validateSnapshot(snap); err != nil {
		return snapshot{}, err
	}
	return snap, nil
}

func readSnapshotFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	// Closing a read-only snapshot has no pending writes to finalize.
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating store: %w", err)
	}
	if info.Size() > maxSnapshotBytes {
		return nil, ErrStoreTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading store: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return nil, ErrStoreTooLarge
	}
	return data, nil
}

func newSnapshot() snapshot {
	return snapshot{JournalSequence: 0, SchemaVersion: storeSchemaVersion, UpdatedAt: time.Time{}, Sessions: make(map[string]Session)}
}

func writeSnapshotAtomic(path string, snap snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding store: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSnapshotBytes {
		return ErrStoreTooLarge
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp store: %w", err)
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		if keep {
			return
		}
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("setting temp store permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("writing temp store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("syncing temp store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing temp store: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("renaming temp store: %w", err)
	}
	keep = true
	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening state directory: %w", err)
	}
	defer func() { _ = handle.Close() }()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("syncing state directory: %w", err)
	}
	return nil
}
