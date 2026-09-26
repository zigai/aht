package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultPersistenceSettle   = 25 * time.Millisecond
	defaultPersistenceMaxDelay = 250 * time.Millisecond
	// defaultBackgroundPersistInterval bounds how long state that consumers
	// cannot see, such as refreshed evidence timestamps, stays memory-only.
	defaultBackgroundPersistInterval = time.Minute

	// DefaultTombstoneTTL is how long a MemoryStore keeps an identified gone
	// session so late native reports from the ended incarnation are rejected.
	DefaultTombstoneTTL = 10 * time.Minute
)

var _ Store = (*MemoryStore)(nil)

// StateSnapshot is an immutable, filtered view of the effective registry state.
// Revision advances only when state visible to consumers changes; observation
// heartbeats that merely refresh evidence timestamps do not advance it.
type StateSnapshot struct {
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Sessions  []Session `json:"sessions"`
}

// MemoryStoreOptions configures an in-memory authoritative store.
type MemoryStoreOptions struct {
	// TombstoneTTL is how long an identified gone session stays in the registry
	// to reject late native reports from its ended incarnation. Values <= 0 use
	// DefaultTombstoneTTL. Process-only gone sessions are removed immediately.
	TombstoneTTL time.Duration
}

// MemoryStore keeps the authoritative registry in memory while retaining the
// same evidence reducer and query contract as FileStore.
//
// The registry holds current state only. A gone session with only process
// identity is removed when it goes gone; an identified gone session is removed
// once it has been gone for the tombstone TTL. Expiry runs when the store
// opens, after every command, and from RunPersistence.
type MemoryStore struct {
	mu sync.RWMutex

	reducer      Reducer
	path         string
	now          func() time.Time
	tombstoneTTL time.Duration
	snapshot     snapshot
	revision     uint64
	// storageRevision counts every in-memory change. visibleRevision is the
	// storageRevision of the latest consumer-visible change; only those
	// schedule a prompt snapshot write.
	storageRevision           uint64
	visibleRevision           uint64
	persistedRevision         uint64
	persistedAt               time.Time
	backgroundPersistInterval time.Duration
	stateChanged              chan struct{}
	owner                     *storeLock
	dirty                     chan struct{}
	flush                     chan struct{}
}

// OpenMemoryStore loads path once and returns an in-memory authoritative store
// that uses DefaultTombstoneTTL.
func OpenMemoryStore(path string, rules Rules) (*MemoryStore, error) {
	return OpenMemoryStoreWithOptions(path, rules, MemoryStoreOptions{TombstoneTTL: 0})
}

// OpenMemoryStoreWithOptions loads path once, removes expired tombstones, and
// returns an in-memory authoritative store.
func OpenMemoryStoreWithOptions(path string, rules Rules, options MemoryStoreOptions) (*MemoryStore, error) {
	ttl := options.TombstoneTTL
	if ttl <= 0 {
		ttl = DefaultTombstoneTTL
	}
	fileStore := NewFileStore(path, rules)
	if err := os.MkdirAll(filepath.Dir(fileStore.Path()), 0o700); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}
	owner, err := tryStoreLock(fileStore.Path() + ".owner.lock")
	if err != nil {
		return nil, err
	}
	loaded, err := fileStore.load()
	if err != nil {
		return nil, closeStoreLock(owner, err)
	}
	store := &MemoryStore{
		mu: sync.RWMutex{}, reducer: NewReducer(rules), path: fileStore.Path(), now: func() time.Time { return time.Now().UTC() },
		tombstoneTTL: ttl, snapshot: cloneRegistrySnapshot(loaded), revision: 1,
		storageRevision: 1, visibleRevision: 1, persistedRevision: 0, persistedAt: time.Time{}, backgroundPersistInterval: defaultBackgroundPersistInterval,
		stateChanged: make(chan struct{}), owner: owner, dirty: make(chan struct{}, 1), flush: make(chan struct{}, 1),
	}
	expireTombstones(store.snapshot.Sessions, store.now(), ttl)
	if err := store.Flush(context.Background()); err != nil {
		return nil, closeStoreLock(owner, err)
	}
	return store, nil
}

func (s *MemoryStore) Close() error {
	err := s.Flush(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		err = closeStoreLock(s.owner, err)
		s.owner = nil
	}
	return err
}

// Path returns the durable snapshot path associated with the store.
func (s *MemoryStore) Path() string { return s.path }

// Observe records one observation atomically.
func (s *MemoryStore) Observe(ctx context.Context, observation Observation) (Session, error) {
	sessions, err := s.ObserveBatch(ctx, []Observation{observation})
	if err != nil {
		return Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}

	s.mu.RLock()
	id := findMatchingSession(s.snapshot.Sessions, observation)
	if id == "" {
		id = sessionIDForObservation(observation)
	}
	session, ok := s.snapshot.Sessions[id]
	s.mu.RUnlock()
	if !ok {
		return Session{}, ErrSessionNotFound
	}

	return cloneSessionValue(session), nil
}

// ObserveBatch atomically reduces observations into memory and notifies state
// subscribers only when the effective consumer-visible state changes.
func (s *MemoryStore) ObserveBatch(ctx context.Context, observations []Observation) ([]Session, error) {
	result, err := s.command(ctx, journalEntry{Sequence: 0, ReceivedAt: time.Time{}, Observations: observations, DeleteAfter: nil, Reset: false})
	if err != nil {
		return nil, err
	}
	saved := make([]Session, len(result.sessions))
	for index := range saved {
		saved[index] = cloneSessionValue(result.sessions[index])
	}
	return saved, nil
}

// List returns a defensive copy of all sessions matching filter.
func (s *MemoryStore) List(ctx context.Context, filter Filter) ([]Session, error) {
	state, err := s.State(ctx, filter)
	if err != nil {
		return nil, err
	}

	return state.Sessions, nil
}

// Get returns a defensive copy of one session.
func (s *MemoryStore) Get(ctx context.Context, id string) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, fmt.Errorf("checking context: %w", err)
	}

	s.mu.Lock()
	if err := s.drainLocked(ctx); err != nil {
		s.mu.Unlock()
		return Session{}, err
	}
	session, ok := s.snapshot.Sessions[id]
	if !ok {
		s.mu.Unlock()
		return Session{}, ErrSessionNotFound
	}
	s.mu.Unlock()

	session = cloneSessionValue(session)
	session.SchemaVersion = storeSchemaVersion

	return session, nil
}

// SummaryWithOptions returns summaries computed from one in-memory snapshot with options.
func (s *MemoryStore) SummaryWithOptions(ctx context.Context, filter Filter, opts SummaryOptions) ([]Summary, error) {
	if opts.GroupBy != "" && !opts.GroupBy.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedGroupBy, opts.GroupBy)
	}
	sessions, err := s.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return SummariesWithOptions(sessions, opts), nil
}

// GC removes expired gone-session tombstones from memory.
func (s *MemoryStore) GC(ctx context.Context, deleteAfter time.Duration) (GCResult, error) {
	result, err := s.command(ctx, journalEntry{Sequence: 0, ReceivedAt: time.Time{}, Observations: nil, DeleteAfter: &deleteAfter, Reset: false})
	return result.gc, err
}

// State returns the latest effective state and its monotonic revision.
func (s *MemoryStore) State(ctx context.Context, filter Filter) (StateSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return StateSnapshot{}, fmt.Errorf("checking context: %w", err)
	}

	s.mu.Lock()
	if err := s.drainLocked(ctx); err != nil {
		s.mu.Unlock()
		return StateSnapshot{}, err
	}
	state := s.stateLocked(filter)
	s.mu.Unlock()
	return state, nil
}

// WaitForRevision blocks until a state revision newer than after is available.
func (s *MemoryStore) WaitForRevision(ctx context.Context, after uint64, filter Filter) (StateSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return StateSnapshot{}, fmt.Errorf("waiting for registry revision: %w", err)
		}

		s.mu.Lock()
		if err := s.drainLocked(ctx); err != nil {
			s.mu.Unlock()
			return StateSnapshot{}, err
		}
		if s.revision > after {
			state := s.stateLocked(filter)
			s.mu.Unlock()

			return state, nil
		}
		changed := s.stateChanged
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return StateSnapshot{}, fmt.Errorf("waiting for registry revision: %w", ctx.Err())
		case <-changed:
		case <-time.After(defaultPersistenceSettle):
		}
	}
}

//nolint:funcorder // lock-scoped snapshot construction stays beside its callers
func (s *MemoryStore) stateLocked(filter Filter) StateSnapshot {
	sessions := make([]Session, 0, len(s.snapshot.Sessions))
	for _, stored := range s.snapshot.Sessions {
		session := cloneSessionValue(stored)
		session.SchemaVersion = storeSchemaVersion
		sessions = append(sessions, session)
	}

	return StateSnapshot{
		Revision:  s.revision,
		UpdatedAt: s.snapshot.UpdatedAt,
		Sessions:  FilterSessions(sessions, filter),
	}
}

// Flush atomically persists the latest in-memory snapshot, including state
// that is not visible to consumers.
func (s *MemoryStore) Flush(ctx context.Context) error {
	return s.persist(ctx, true)
}

// persist checkpoints the snapshot and truncates the journal. Unless force is
// set, it writes only after a consumer-visible change or, for evidence-only
// refreshes, once the background interval has elapsed since the last write.
//
//nolint:funcorder // persistence policy stays beside Flush
func (s *MemoryStore) persist(ctx context.Context, force bool) error {
	select {
	case s.flush <- struct{}{}:
		defer func() { <-s.flush }()
	case <-ctx.Done():
		return fmt.Errorf("waiting to flush registry: %w", ctx.Err())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil {
		return nil
	}
	lock, err := openStoreLock(ctx, s.path+".lock", nil)
	if err != nil {
		return err
	}
	if err := s.consumeJournalLocked(ctx); err != nil {
		return closeStoreLock(lock, err)
	}
	s.expireTombstonesLocked()
	if !s.persistDueLocked(force) {
		return closeStoreLock(lock, nil)
	}
	if err := ctx.Err(); err != nil {
		return closeStoreLock(lock, fmt.Errorf("flushing registry: %w", err))
	}
	if err := persistJournalSnapshot(s.path, s.snapshot); err != nil {
		return closeStoreLock(lock, err)
	}
	s.persistedRevision = s.storageRevision
	s.persistedAt = time.Now()
	return closeStoreLock(lock, nil)
}

//nolint:funcorder // persistence policy stays beside Flush
func (s *MemoryStore) persistDueLocked(force bool) bool {
	if s.storageRevision <= s.persistedRevision {
		return false
	}
	if force || s.visibleRevision > s.persistedRevision {
		return true
	}
	return time.Since(s.persistedAt) >= s.backgroundPersistInterval
}

//nolint:funcorder // lock-scoped expiry stays beside persistence
func (s *MemoryStore) expireTombstonesLocked() {
	now := s.now().UTC()
	if !hasExpiredTombstones(s.snapshot.Sessions, now, s.tombstoneTTL) {
		return
	}
	candidate := cloneRegistrySnapshotForMutation(s.snapshot)
	expireTombstones(candidate.Sessions, now, s.tombstoneTTL)
	changes := stateChanges(State{Sessions: s.snapshot.Sessions, UpdatedAt: s.snapshot.UpdatedAt}, State{Sessions: candidate.Sessions, UpdatedAt: candidate.UpdatedAt})
	s.acceptSnapshotLocked(candidate, changes)
}

func (s *MemoryStore) Reset(ctx context.Context) (ResetResult, error) {
	result, err := s.command(ctx, journalEntry{Sequence: 0, ReceivedAt: time.Time{}, Observations: nil, DeleteAfter: nil, Reset: true})
	if err != nil {
		return ResetResult{}, err
	}
	return result.reset, s.Flush(ctx)
}

// RunPersistence coalesces bursts of consumer-visible changes into durable
// atomic snapshots and expires tombstones. Evidence-only refreshes, such as
// observer heartbeats, are neither journaled nor written promptly; they are
// persisted with the next visible change, on a slow background interval, or on
// shutdown. The caller owns this loop and must cancel ctx before discarding the
// store.
func (s *MemoryStore) RunPersistence(ctx context.Context, settle, maximumDelay time.Duration) error {
	settle, maximumDelay = normalizePersistenceOptions(settle, maximumDelay)

	for {
		select {
		case <-ctx.Done():
			return s.flushOnShutdown(ctx)
		case <-s.dirty:
		case <-time.After(defaultPersistenceMaxDelay):
			if err := s.persist(ctx, false); err != nil {
				if ctx.Err() != nil {
					return s.flushOnShutdown(ctx)
				}
				return fmt.Errorf("persisting registry snapshot: %w", err)
			}
			continue
		}

		if err := s.coalescePersistence(ctx, settle, maximumDelay); err != nil {
			return s.flushOnShutdown(ctx)
		}
		if err := s.persist(ctx, false); err != nil {
			if ctx.Err() != nil {
				return s.flushOnShutdown(ctx)
			}
			return fmt.Errorf("persisting registry snapshot: %w", err)
		}
	}
}

func validateNativePayloadChanges(previous, candidate snapshot) error {
	for id, session := range candidate.Sessions {
		native := session.Observations.Native
		// The reducer replaces native observations on change. Unchanged pointers
		// belong to already validated state; only final retained payloads matter.
		if native == nil || native == previous.Sessions[id].Observations.Native || len(native.RawPayload) == 0 || json.Valid(native.RawPayload) {
			continue
		}
		// Ask the same codec used by persistence to construct its MarshalerError.
		// Valid payloads require no serialization or encoded copy on this path.
		_, err := json.Marshal(session)
		return fmt.Errorf("encoding store: %w", err)
	}
	return nil
}

func normalizePersistenceOptions(settle, maximumDelay time.Duration) (time.Duration, time.Duration) {
	if settle <= 0 {
		settle = defaultPersistenceSettle
	}
	if maximumDelay < settle {
		maximumDelay = defaultPersistenceMaxDelay
	}
	return settle, maximumDelay
}

func (s *MemoryStore) setNowForTest(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
}

func (s *MemoryStore) flushOnShutdown(ctx context.Context) error {
	if err := s.Flush(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("persisting final registry snapshot: %w", err)
	}

	return nil
}

func (s *MemoryStore) signalDirty() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

func (s *MemoryStore) coalescePersistence(ctx context.Context, settle, maximumDelay time.Duration) error {
	settleTimer := time.NewTimer(settle)
	defer settleTimer.Stop()
	maximumTimer := time.NewTimer(maximumDelay)
	defer maximumTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting to persist: %w", ctx.Err())
		case <-s.dirty:
			settleTimer.Reset(settle)
		case <-settleTimer.C:
			return nil
		case <-maximumTimer.C:
			return nil
		}
	}
}
