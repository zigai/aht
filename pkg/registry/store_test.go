package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestStoreBatchIsAtomicOnSourceConflict(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	idle := ActivityIdle
	first, err := store.Observe(context.Background(), Observation{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "atomic"}, Activity: &idle, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	running := ActivityRunning
	_, err = store.ObserveBatch(context.Background(), []Observation{
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "atomic"}, Activity: &running, ObservedAt: at.Add(time.Second)},
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "atomic"}, Activity: &idle, ObservedAt: at.Add(time.Second)},
	})
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("expected source conflict, got %v", err)
	}
	session, err := store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity == nil || *session.Activity != ActivityIdle {
		t.Fatalf("failed batch partially committed: %#v", session)
	}
}

func TestStoreConcurrentWritersPreserveEverySession(t *testing.T) {
	t.Parallel()

	const writerCount = 32
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	start := make(chan struct{})
	errs := make(chan error, writerCount)
	var writers sync.WaitGroup
	writers.Add(writerCount)
	for index := range writerCount {
		go func() {
			defer writers.Done()
			<-start
			_, err := store.Observe(context.Background(), Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "concurrent-" + strconv.Itoa(index)},
				NativeEvent: "start", ObservedAt: time.Now().UTC(),
			})
			errs <- err
		}()
	}
	close(start)
	writers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != writerCount {
		t.Fatalf("sessions = %d, want %d; concurrent writes lost data", len(sessions), writerCount)
	}
}

func TestStoreListFiltersIndependentDimensions(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	start := NativeLifecycleStart
	idle := ActivityIdle
	for _, observation := range []Observation{
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "live-idle"}, Lifecycle: &start, Activity: &idle, ObservedAt: at},
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessClaude, Identity: ObservationIdentity{SessionID: "unknown"}, ObservedAt: at},
	} {
		if _, err := store.Observe(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
	}
	present := true
	if _, err := store.Observe(context.Background(), Observation{Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence, Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "live-idle"}, ProcessPresent: &present, Process: &ProcessIdentity{PID: 7, StartIdentity: "boot:7"}, ObservedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	end := NativeLifecycleEnd
	if _, err := store.Observe(context.Background(), Observation{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessClaude, Identity: ObservationIdentity{SessionID: "unknown"}, Lifecycle: &end, ObservedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	live, err := store.List(context.Background(), Filter{Presence: PresenceLive, Activity: ActivityIdle})
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ID == "" {
		t.Fatalf("unexpected independent filter result: %#v", live)
	}
	gone, err := store.List(context.Background(), Filter{Presence: PresenceGone})
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0].Activity != nil {
		t.Fatalf("gone activity must be null: %#v", gone)
	}
}

func TestStorePersistsSchemaV2Envelope(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := NewFileStore(path)
	at := time.Now().UTC().Add(-time.Minute)
	if _, err := store.Observe(context.Background(), Observation{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "json"}, ObservedAt: at}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Version  int                `json:"schema_version"`
		Sessions map[string]Session `json:"sessions"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Version != 2 || len(envelope.Sessions) != 1 {
		t.Fatalf("unexpected schema envelope: %#v", envelope)
	}
}

func TestStorePersistsNativeMultiplexerLocation(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/codex", CWD: "/repo"}
	present := true
	if _, err := store.Observe(context.Background(), Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence, Harness: HarnessCodex,
		ProcessPresent: &present, Process: process, ObservedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	location := &MultiplexerContext{Kind: MultiplexerZellij, SessionName: "work", TabID: "3", PaneID: "terminal_7", PaneCurrentPath: "/repo"}
	session, err := store.Observe(context.Background(), Observation{
		Source: ObservationSourceMultiplexer, Evidence: ObservationEvidenceMultiplexerLocation, Harness: HarnessCodex,
		Process: process, Multiplexer: location, ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Multiplexer != *location || !session.Tmux.Empty() || session.Observations.Multiplexer == nil || session.Observations.Multiplexer.Context != *location {
		t.Fatalf("stored session = %#v", session)
	}
}

func TestStoreResetRecoversMalformedState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"sessions":`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileStore(path)
	result, err := store.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset() error = %v", err)
	}
	if result.Cleared != 0 || result.Remaining != 0 {
		t.Fatalf("Reset() result = %#v", result)
	}
	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List() after reset error = %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("List() after reset = %#v", sessions)
	}
}

func TestStoreRejectsSchemaV1(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(path).List(context.Background(), Filter{})
	unsupportedErr, ok := errors.AsType[*UnsupportedSchemaError](err)
	if !ok {
		t.Fatalf("expected unsupported schema, got %v", err)
	}
	if unsupportedErr.Version != 1 || unsupportedErr.Path != path {
		t.Fatalf("unsupported schema error = %+v, want version 1 path %q", unsupportedErr, path)
	}
}

func TestStoreGCUsesInclusiveAgeBoundary(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	threshold := 10 * time.Minute
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	store.setNowForTest(func() time.Time { return base })
	presence := PresenceGone
	for _, observation := range []Observation{
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "at-threshold"}, Presence: &presence, ObservedAt: base.Add(-threshold)},
		{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "one-nanosecond-newer"}, Presence: &presence, ObservedAt: base.Add(-threshold).Add(time.Nanosecond)},
	} {
		if _, err := store.Observe(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
	}

	result, err := store.GC(context.Background(), threshold)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Remaining != 1 {
		t.Fatalf("GC() result = %#v, want one deleted and one remaining", result)
	}
	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "one-nanosecond-newer" {
		t.Fatalf("GC() deleted wrong boundary session: %#v", sessions)
	}
}

func TestObserveAutomaticallyRemovesExpiredGoneSessions(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	now := base
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	store.setNowForTest(func() time.Time { return now })

	gone := PresenceGone
	if _, err := store.Observe(context.Background(), Observation{
		Source:     ObservationSourceNative,
		Evidence:   ObservationEvidenceNativeEvent,
		Harness:    HarnessCodex,
		Identity:   ObservationIdentity{SessionID: "ended"},
		Presence:   &gone,
		ObservedAt: base.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	live := PresenceLive
	observeLive := func() {
		t.Helper()

		if _, err := store.Observe(context.Background(), Observation{
			Source:     ObservationSourceNative,
			Evidence:   ObservationEvidenceNativeEvent,
			Harness:    HarnessCodex,
			Identity:   ObservationIdentity{SessionID: "current"},
			Presence:   &live,
			ObservedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	now = base.Add(automaticGoneRetention - time.Nanosecond)
	observeLive()

	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions before retention boundary = %#v, want recent tombstone retained", sessions)
	}

	now = base.Add(automaticGoneRetention)
	observeLive()

	sessions, err = store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "current" {
		t.Fatalf("sessions after automatic cleanup = %#v, want only current live session", sessions)
	}
}

func TestSummariesCountIndependentPresenceAndActivity(t *testing.T) {
	t.Parallel()

	activity := func(value Activity) *Activity { return &value }
	sessions := []Session{
		{Presence: PresenceLive, Activity: activity(ActivityRunning), Tmux: TmuxContext{SessionID: "$1", SessionName: "work"}},
		{Presence: PresenceLive, Activity: activity(ActivityWaiting), Tmux: TmuxContext{SessionID: "$1", SessionName: "work"}},
		{Presence: PresenceLive, Activity: activity(ActivityIdle), Tmux: TmuxContext{SessionID: "$1", SessionName: "work"}},
		{Presence: PresenceGone, Activity: nil, Tmux: TmuxContext{SessionID: "$1", SessionName: "work"}},
		{Presence: PresenceUnknown, Activity: activity(ActivityUnknown)},
	}
	summaries := summariesForSessions(sessions)
	if len(summaries) != 2 {
		t.Fatalf("summaries = %#v, want work and unknown groups", summaries)
	}
	work := summaries[0]
	if work.Total != 4 || work.Live != 3 || work.Gone != 1 || work.Running != 1 || work.Waiting != 1 || work.Idle != 1 || work.ActivityUnknown != 0 {
		t.Fatalf("work summary = %#v", work)
	}
	unknown := summaries[1]
	if unknown.Total != 1 || unknown.PresenceUnknown != 1 || unknown.ActivityUnknown != 1 {
		t.Fatalf("unknown summary = %#v", unknown)
	}
}

func TestSummariesGroupNativeMultiplexerSessions(t *testing.T) {
	t.Parallel()
	activity := ActivityWaiting
	summaries := summariesForSessions([]Session{{
		Presence: PresenceLive, Activity: &activity,
		Multiplexer: MultiplexerContext{Kind: MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"},
	}})
	if len(summaries) != 1 || summaries[0].MultiplexerKind != MultiplexerZellij || summaries[0].MultiplexerSessionName != "work" || summaries[0].Total != 1 || summaries[0].Waiting != 1 {
		t.Fatalf("multiplexer summaries = %#v", summaries)
	}
}

func TestObservationValidateRejectsCorruptBoundaryValues(t *testing.T) {
	t.Parallel()

	base := Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "session"},
		NativeEvent: "start", ObservedAt: time.Now().UTC(),
	}
	tests := []struct {
		name   string
		mutate func(*Observation)
	}{
		{name: "unknown harness", mutate: func(observation *Observation) { observation.Harness = Harness("unknown") }},
		{name: "noncanonical harness", mutate: func(observation *Observation) { observation.Harness = Harness("claude-code") }},
		{name: "invalid lifecycle", mutate: func(observation *Observation) { value := NativeLifecycle("restart"); observation.Lifecycle = &value }},
		{name: "invalid presence", mutate: func(observation *Observation) { value := Presence("present"); observation.Presence = &value }},
		{name: "invalid activity", mutate: func(observation *Observation) { value := Activity("busy"); observation.Activity = &value }},
		{name: "incomplete process", mutate: func(observation *Observation) { observation.Process = &ProcessIdentity{PID: 42} }},
		{name: "negative parent pid", mutate: func(observation *Observation) {
			observation.Process = &ProcessIdentity{PID: 42, PPID: -1, StartIdentity: "boot:42"}
		}},
		{name: "negative tmux pid", mutate: func(observation *Observation) { observation.Tmux = &TmuxContext{PanePID: -1} }},
		{name: "negative catalog pid", mutate: func(observation *Observation) { observation.Catalog = &CatalogMetadata{ProcessPID: -1} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := base
			test.mutate(&observation)
			if err := observation.Validate(); !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("Observation.Validate() error = %v, want %v", err, ErrInvalidObservation)
			}
		})
	}
}

func TestStoreRejectsCorruptPersistedSessionState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*snapshot)
	}{
		{name: "mismatched map key", mutate: corruptSnapshotMapKey},
		{name: "invalid activity", mutate: corruptSnapshotActivity},
		{name: "incomplete process", mutate: corruptSnapshotProcess},
		{name: "zero observation timestamp", mutate: corruptSnapshotObservationTime},
		{name: "stale native revival corruption", mutate: corruptSnapshotStaleNativeRevival},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := writeCorruptTestStore(t, test.mutate)
			_, err := store.List(context.Background(), Filter{})
			if !errors.Is(err, ErrCorruptStore) {
				t.Fatalf("List() error = %v, want %v", err, ErrCorruptStore)
			}
		})
	}
}

func TestStoreRejectsInvalidMutationBeforePersisting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	store := NewFileStore(path)
	if _, err := store.Observe(context.Background(), Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "session"},
		NativeEvent: "start", ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	err := store.withSnapshot(t.Context(), func(snap *snapshot) error {
		id, session := onlyStoredSession(snap.Sessions)
		session.Activity = nil
		snap.Sessions[id] = session
		return nil
	})
	if !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("withSnapshot() error = %v, want %v", err, ErrCorruptStore)
	}
	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("List() after rejected mutation error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].Activity == nil {
		t.Fatalf("invalid mutation reached disk: %#v", sessions)
	}
}

func writeCorruptTestStore(t *testing.T, mutate func(*snapshot)) *FileStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := NewFileStore(path)
	_, err := store.Observe(context.Background(), Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessCodex, Identity: ObservationIdentity{SessionID: "session"},
		NativeEvent: "start", ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	mutate(&snap)
	data, err = json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return store
}

func corruptSnapshotMapKey(snap *snapshot) {
	id, session := onlyStoredSession(snap.Sessions)
	delete(snap.Sessions, id)
	snap.Sessions["different-id"] = session
}

func corruptSnapshotActivity(snap *snapshot) {
	id, session := onlyStoredSession(snap.Sessions)
	activity := Activity("busy")
	session.Activity = &activity
	snap.Sessions[id] = session
}

func corruptSnapshotProcess(snap *snapshot) {
	id, session := onlyStoredSession(snap.Sessions)
	session.Process = &ProcessIdentity{PID: 42}
	snap.Sessions[id] = session
}

func corruptSnapshotObservationTime(snap *snapshot) {
	id, session := onlyStoredSession(snap.Sessions)
	session.Observations.Native.ObservedAt = time.Time{}
	snap.Sessions[id] = session
}

func corruptSnapshotStaleNativeRevival(snap *snapshot) {
	id, session := onlyStoredSession(snap.Sessions)
	process := ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	nativeAt := session.Observations.Native.ObservedAt
	processAt := nativeAt.Add(time.Second)
	start := NativeLifecycleStart
	live := PresenceLive
	idle := ActivityIdle
	session.Presence = PresenceLive
	session.Activity = nil
	session.Process = &process
	session.PresenceChangedAt = nativeAt
	session.ActivityChangedAt = processAt
	session.Observations.Native.Lifecycle = &start
	session.Observations.Native.Presence = &live
	session.Observations.Native.Activity = &idle
	session.Observations.Native.Process = process
	session.Observations.Process = &ProcessObservation{Present: false, Process: process, ObservedAt: processAt}
	session.ActivityDecision = &ActivityDecision{Authority: "process", Reason: "process_gone", Process: process, ObservedAt: processAt}
	snap.Sessions[id] = session
}

func onlyStoredSession(sessions map[string]Session) (string, Session) {
	for id, session := range sessions {
		return id, session
	}
	return "", Session{}
}
