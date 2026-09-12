package registry

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

const (
	defaultPersistenceSettle   = 25 * time.Millisecond
	defaultPersistenceMaxDelay = 250 * time.Millisecond
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

// MemoryStore keeps the authoritative registry in memory while retaining the
// same evidence reducer and query contract as FileStore.
type MemoryStore struct {
	mu sync.RWMutex

	path              string
	now               func() time.Time
	snapshot          snapshot
	revision          uint64
	storageRevision   uint64
	persistedRevision uint64
	stateChanged      chan struct{}
	dirty             chan struct{}
	flush             chan struct{}
}

// OpenMemoryStore loads path once and returns an in-memory authoritative store.
func OpenMemoryStore(path string) (*MemoryStore, error) {
	fileStore := NewFileStore(path)
	loaded, err := fileStore.load()
	if err != nil {
		return nil, err
	}

	return &MemoryStore{
		mu:                sync.RWMutex{},
		path:              fileStore.Path(),
		now:               func() time.Time { return time.Now().UTC() },
		snapshot:          cloneRegistrySnapshot(loaded),
		revision:          1,
		storageRevision:   0,
		persistedRevision: 0,
		stateChanged:      make(chan struct{}),
		dirty:             make(chan struct{}, 1),
		flush:             make(chan struct{}, 1),
	}, nil
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
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("checking context: %w", err)
	}

	s.mu.Lock()
	receivedAt := s.now().UTC()
	candidate := cloneRegistrySnapshotForMutation(s.snapshot)
	saved, err := applyObservationBatch(ctx, &candidate, observations, receivedAt)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	for _, session := range saved {
		if reason := storedSessionCorruption(session.ID, session); reason != "" {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: session %q: %s", ErrCorruptStore, session.ID, reason)
		}
	}

	stateChanged := !materialSnapshotsEqual(s.snapshot, candidate)
	s.snapshot = candidate
	s.storageRevision++
	if stateChanged {
		s.revision++
		close(s.stateChanged)
		s.stateChanged = make(chan struct{})
	}
	s.mu.Unlock()

	if stateChanged {
		s.signalDirty()
	}

	result := make([]Session, len(saved))
	for index := range saved {
		result[index] = cloneSessionValue(saved[index])
	}

	return result, nil
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

	s.mu.RLock()
	session, ok := s.snapshot.Sessions[id]
	s.mu.RUnlock()
	if !ok {
		return Session{}, ErrSessionNotFound
	}

	session = cloneSessionValue(session)
	session.SchemaVersion = storeSchemaVersion
	populateMultiplexerProjection(&session)

	return session, nil
}

// SummaryByTmuxSession returns summaries computed from one in-memory snapshot.
func (s *MemoryStore) SummaryByTmuxSession(ctx context.Context, filter Filter) ([]Summary, error) {
	sessions, err := s.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	return summariesForSessions(sessions), nil
}

// GC removes expired gone-session tombstones from memory.
func (s *MemoryStore) GC(ctx context.Context, deleteAfter time.Duration) (GCResult, error) {
	if err := ctx.Err(); err != nil {
		return GCResult{}, fmt.Errorf("checking context: %w", err)
	}

	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return GCResult{}, fmt.Errorf("collecting registry: %w", err)
	}
	now := s.now().UTC()
	candidate := cloneRegistrySnapshotForMutation(s.snapshot)
	deleted := deleteExpiredGoneSessions(
		candidate.Sessions,
		now,
		deleteAfter,
		func(session Session) time.Time { return session.PresenceChangedAt },
	)
	if deleted == 0 {
		remaining := len(s.snapshot.Sessions)
		s.mu.Unlock()

		return GCResult{Deleted: 0, Remaining: remaining}, nil
	}

	candidate.UpdatedAt = now
	s.snapshot = candidate
	s.storageRevision++
	s.revision++
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
	remaining := len(candidate.Sessions)
	s.mu.Unlock()

	s.signalDirty()

	return GCResult{Deleted: deleted, Remaining: remaining}, nil
}

// State returns the latest effective state and its monotonic revision.
func (s *MemoryStore) State(ctx context.Context, filter Filter) (StateSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return StateSnapshot{}, fmt.Errorf("checking context: %w", err)
	}

	s.mu.RLock()
	state := s.stateLocked(filter)
	s.mu.RUnlock()

	return state, nil
}

// WaitForRevision blocks until a state revision newer than after is available.
func (s *MemoryStore) WaitForRevision(ctx context.Context, after uint64, filter Filter) (StateSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return StateSnapshot{}, fmt.Errorf("waiting for registry revision: %w", err)
		}

		s.mu.RLock()
		if s.revision > after {
			state := s.stateLocked(filter)
			s.mu.RUnlock()

			return state, nil
		}
		changed := s.stateChanged
		s.mu.RUnlock()

		select {
		case <-ctx.Done():
			return StateSnapshot{}, fmt.Errorf("waiting for registry revision: %w", ctx.Err())
		case <-changed:
		}
	}
}

//nolint:funcorder // lock-scoped snapshot construction stays beside its callers
func (s *MemoryStore) stateLocked(filter Filter) StateSnapshot {
	sessions := make([]Session, 0, len(s.snapshot.Sessions))
	for _, stored := range s.snapshot.Sessions {
		session := cloneSessionValue(stored)
		session.SchemaVersion = storeSchemaVersion
		populateMultiplexerProjection(&session)
		sessions = append(sessions, session)
	}

	return StateSnapshot{
		Revision:  s.revision,
		UpdatedAt: s.snapshot.UpdatedAt,
		Sessions:  filterSessions(sessions, filter),
	}
}

// Flush atomically persists the latest in-memory snapshot.
func (s *MemoryStore) Flush(ctx context.Context) error {
	// Serialize the complete read/write/acknowledge transaction, not only its
	// snapshot copy: an older concurrent flush must never replace a newer one.
	select {
	case s.flush <- struct{}{}:
		defer func() { <-s.flush }()
	case <-ctx.Done():
		return fmt.Errorf("waiting to flush registry: %w", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flushing registry: %w", err)
	}

	s.mu.RLock()
	if s.storageRevision <= s.persistedRevision {
		s.mu.RUnlock()
		return nil
	}
	revision := s.storageRevision
	snapshot := cloneRegistrySnapshot(s.snapshot)
	s.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flushing registry: %w", err)
	}
	if err := writeSnapshotAtomic(s.path, snapshot); err != nil {
		return err
	}

	s.mu.Lock()
	if revision > s.persistedRevision {
		s.persistedRevision = revision
	}
	dirty := s.storageRevision > s.persistedRevision
	s.mu.Unlock()
	if dirty {
		s.signalDirty()
	}

	return nil
}

// RunPersistence coalesces bursts of observations into durable atomic snapshots.
// The caller owns this loop and must cancel ctx before discarding the store.
func (s *MemoryStore) RunPersistence(ctx context.Context, settle, maximumDelay time.Duration) error {
	settle, maximumDelay = normalizePersistenceOptions(settle, maximumDelay)

	for {
		select {
		case <-ctx.Done():
			return s.flushOnShutdown(ctx)
		case <-s.dirty:
		}

		settleTimer := time.NewTimer(settle)
		maximumTimer := time.NewTimer(maximumDelay)
		ready := false
		for !ready {
			select {
			case <-ctx.Done():
				settleTimer.Stop()
				maximumTimer.Stop()

				return s.flushOnShutdown(ctx)
			case <-s.dirty:
				settleTimer.Reset(settle)
			case <-settleTimer.C:
				ready = true
			case <-maximumTimer.C:
				ready = true
			}
		}
		settleTimer.Stop()
		maximumTimer.Stop()
		if err := s.Flush(ctx); err != nil {
			if ctx.Err() != nil {
				return s.flushOnShutdown(ctx)
			}
			return fmt.Errorf("persisting registry snapshot: %w", err)
		}
	}
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

func cloneRegistrySnapshotForMutation(source snapshot) snapshot {
	return snapshot{
		SchemaVersion: source.SchemaVersion,
		LegacyVersion: nil,
		UpdatedAt:     source.UpdatedAt,
		// The reducer treats Session values as copy-on-write and replaces every
		// nested pointer or slice it changes, so cloning the map is sufficient
		// for atomic rollback without copying every unaffected session.
		Sessions: maps.Clone(source.Sessions),
	}
}

func cloneRegistrySnapshot(source snapshot) snapshot {
	cloned := snapshot{
		SchemaVersion: source.SchemaVersion,
		LegacyVersion: nil,
		UpdatedAt:     source.UpdatedAt,
		Sessions:      make(map[string]Session, len(source.Sessions)),
	}
	for id, session := range source.Sessions {
		cloned.Sessions[id] = cloneSessionValue(session)
	}

	return cloned
}

func cloneSessionValue(source Session) Session {
	cloned := source
	cloned.Activity = clonePtr(source.Activity)
	cloned.ResumeCommand = append([]string(nil), source.ResumeCommand...)
	if source.Process != nil {
		process := *source.Process
		cloned.Process = &process
	}
	cloned.Observations = cloneObservations(source.Observations)
	if source.ActivityDecision != nil {
		decision := *source.ActivityDecision
		cloned.ActivityDecision = &decision
	}

	return cloned
}

func cloneObservations(source Observations) Observations {
	var cloned Observations
	if source.Native != nil {
		native := *source.Native
		native.Lifecycle = clonePtr(source.Native.Lifecycle)
		native.Presence = clonePtr(source.Native.Presence)
		native.Activity = clonePtr(source.Native.Activity)
		native.ActivityAuthoritative = clonePtr(source.Native.ActivityAuthoritative)
		native.Sequence = clonePtr(source.Native.Sequence)
		native.Attributes = cloneAttributes(source.Native.Attributes)
		native.RawPayload = cloneRaw(source.Native.RawPayload)
		cloned.Native = &native
	}
	if source.Process != nil {
		process := *source.Process
		cloned.Process = &process
	}
	if source.Tmux != nil {
		tmux := *source.Tmux
		cloned.Tmux = &tmux
	}
	if source.Multiplexer != nil {
		multiplexer := *source.Multiplexer
		cloned.Multiplexer = &multiplexer
	}
	if source.Catalog != nil {
		catalog := *source.Catalog
		catalog.ResumeCommand = append([]string(nil), source.Catalog.ResumeCommand...)
		cloned.Catalog = &catalog
	}
	if source.Screen != nil {
		screen := *source.Screen
		cloned.Screen = &screen
	}

	return cloned
}

func materialSnapshotsEqual(left, right snapshot) bool {
	if len(left.Sessions) != len(right.Sessions) {
		return false
	}
	for id, leftSession := range left.Sessions {
		rightSession, ok := right.Sessions[id]
		if !ok || !materialSessionsEqual(leftSession, rightSession) {
			return false
		}
	}

	return true
}

//nolint:cyclop // Explicit field comparison avoids reflection in the update hot path.
func materialSessionsEqual(left, right Session) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.ID == right.ID &&
		left.Harness == right.Harness &&
		left.Presence == right.Presence &&
		activityEqual(left.Activity, right.Activity) &&
		left.SessionID == right.SessionID &&
		left.SessionPath == right.SessionPath &&
		slices.Equal(left.ResumeCommand, right.ResumeCommand) &&
		left.CWD == right.CWD &&
		left.ProjectRoot == right.ProjectRoot &&
		processPointersEqual(left.Process, right.Process) &&
		left.Tmux == right.Tmux &&
		left.Multiplexer == right.Multiplexer &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.PresenceChangedAt.Equal(right.PresenceChangedAt) &&
		left.ActivityChangedAt.Equal(right.ActivityChangedAt) &&
		materialDecisionsEqual(left.ActivityDecision, right.ActivityDecision)
}

func processPointersEqual(left, right *ProcessIdentity) bool {
	if left == nil || right == nil {
		return left == right
	}

	return *left == *right
}

func materialDecisionsEqual(left, right *ActivityDecision) bool {
	if left == nil || right == nil {
		return left == right
	}

	return left.Authority == right.Authority &&
		left.Reason == right.Reason &&
		left.RuleID == right.RuleID &&
		left.ManifestSource == right.ManifestSource &&
		left.ManifestVersion == right.ManifestVersion &&
		left.FallbackReason == right.FallbackReason &&
		left.Process == right.Process
}
