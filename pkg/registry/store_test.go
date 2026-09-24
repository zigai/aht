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
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	idle := ActivityIdle
	first, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: "atomic"}, Evidence: &Report{Event: "test", Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}
	running := ActivityRunning
	_, err = store.ObserveBatch(context.Background(), []Observation{
		{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "atomic"}, Evidence: &Report{Event: "test", Activity: &running}},
		{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "atomic"}, Evidence: &Report{Event: "test", Activity: &idle}},
	})
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("expected source conflict, got %v", err)
	}
	session, err := store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityIdle {
		t.Fatalf("failed batch partially committed: %#v", session)
	}
}

func TestStoreConcurrentWritersPreserveEverySession(t *testing.T) {
	t.Parallel()

	const writerCount = 32
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	start := make(chan struct{})
	errs := make(chan error, writerCount)
	var writers sync.WaitGroup
	writers.Add(writerCount)
	for index := range writerCount {
		go func() {
			defer writers.Done()
			<-start
			_, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "concurrent-" + strconv.Itoa(index)}, Evidence: &Report{Event: "start"}})
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
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	start := NativeLifecycleStart
	idle := ActivityIdle
	for _, observation := range []Observation{
		{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: "live-idle"}, Evidence: &Report{Event: "test", Lifecycle: &start, Activity: &idle}},
		{Harness: HarnessClaude, At: at, Subject: ObservationIdentity{SessionID: "unknown"}, Evidence: &Report{Event: "test"}},
	} {
		if _, err := store.Observe(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
	}
	present := true
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "live-idle"}, Evidence: &Sighting{Process: ProcessIdentity{PID: 7, StartIdentity: "boot:7"}, Present: present}}); err != nil {
		t.Fatal(err)
	}
	end := NativeLifecycleEnd
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessClaude, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "unknown"}, Evidence: &Report{Event: "test", Lifecycle: &end}}); err != nil {
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
	if len(gone) != 1 || gone[0].Activity() != nil {
		t.Fatalf("gone activity must be null: %#v", gone)
	}
}

func TestStorePersistsSchemaV3Envelope(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := NewJournal(path, fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: "json"}, Evidence: &Report{Event: "test"}}); err != nil {
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
	if envelope.Version != 3 || len(envelope.Sessions) != 1 {
		t.Fatalf("unexpected schema envelope: %#v", envelope)
	}
}

func TestStorePersistsNativeMultiplexerLocation(t *testing.T) {
	t.Parallel()
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/codex", CWD: "/repo"}
	present := true
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{}, Evidence: &Sighting{Process: *process, Present: present}}); err != nil {
		t.Fatal(err)
	}
	location := &Location{Kind: MultiplexerZellij, SessionName: "work", TabID: "3", PaneID: "terminal_7", PaneCurrentPath: "/repo"}
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{}, Evidence: &Placement{Process: *process, Location: *location}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Location != *location || session.Observations.Location == nil || session.Observations.Location.Context != *location {
		t.Fatalf("stored session = %#v", session)
	}
}

func TestStoreResetRecoversMalformedState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"sessions":`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewJournal(path, fixtureRules{})
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
	_, err := NewJournal(path, fixtureRules{}).List(context.Background(), Filter{})
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
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	store.setNowForTest(func() time.Time { return base })
	presence := PresenceGone
	for _, observation := range []Observation{
		{Harness: HarnessCodex, At: base.Add(-threshold), Subject: ObservationIdentity{SessionID: "at-threshold"}, Evidence: &Report{Claim: &presence}},
		{Harness: HarnessCodex, At: base.Add(-threshold).Add(time.Nanosecond), Subject: ObservationIdentity{SessionID: "one-nanosecond-newer"}, Evidence: &Report{Claim: &presence}},
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

func TestObservePreservesGoneSessionsWithoutGC(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	now := base
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	store.setNowForTest(func() time.Time { return now })

	gone := PresenceGone
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base.Add(-time.Hour), Subject: ObservationIdentity{SessionID: "ended"}, Evidence: &Report{Claim: &gone}}); err != nil {
		t.Fatal(err)
	}

	now = base.Add(24 * time.Hour)
	live := PresenceLive
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: now, Subject: ObservationIdentity{SessionID: "current"}, Evidence: &Report{Claim: &live}}); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions after observation batch = %d, want both retained without explicit GC", len(sessions))
	}
}

func TestSummariesCountIndependentPresenceAndActivity(t *testing.T) {
	t.Parallel()

	activity := func(value Activity) *Activity { return &value }
	sessions := []Session{
		{
			Location: Location{Kind: MultiplexerTmux, SessionID: "$1", SessionName: "work"},
			Liveness: NewLiveness(PresenceLive, ActivityValue(activity(ActivityRunning)), nil),
		},
		{
			Location: Location{Kind: MultiplexerTmux, SessionID: "$1", SessionName: "work"},
			Liveness: NewLiveness(PresenceLive, ActivityValue(activity(ActivityWaiting)), nil),
		},
		{
			Location: Location{Kind: MultiplexerTmux, SessionID: "$1", SessionName: "work"},
			Liveness: NewLiveness(PresenceLive, ActivityValue(activity(ActivityIdle)), nil),
		},
		{
			Location: Location{Kind: MultiplexerTmux, SessionID: "$1", SessionName: "work"},
			Liveness: NewLiveness(PresenceGone, ActivityValue(nil), nil),
		},
		{
			Liveness: NewLiveness(PresenceUnknown, ActivityValue(activity(ActivityUnknown)), nil),
		},
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
		Location: Location{Kind: MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"},
		Liveness: NewLiveness(PresenceLive, ActivityValue(&activity), nil),
	}})
	if len(summaries) != 1 || summaries[0].MultiplexerKind != MultiplexerZellij || summaries[0].MultiplexerSessionName != "work" || summaries[0].Total != 1 || summaries[0].Waiting != 1 {
		t.Fatalf("multiplexer summaries = %#v", summaries)
	}
}

func TestObservationValidateRejectsCorruptBoundaryValues(t *testing.T) {
	t.Parallel()

	base := Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "session"}, Evidence: &Report{Event: "start"}}
	tests := []struct {
		name   string
		mutate func(*Observation)
	}{
		{name: "unknown harness", mutate: func(observation *Observation) { observation.Harness = Harness("unknown") }},
		{name: "noncanonical harness", mutate: func(observation *Observation) { observation.Harness = Harness("claude-code") }},
		{name: "invalid lifecycle", mutate: func(observation *Observation) {
			value := NativeLifecycle("restart")
			observation.Report().Lifecycle = &value
		}},
		{name: "invalid presence", mutate: func(observation *Observation) { value := Presence("present"); observation.Report().Claim = &value }},
		{name: "invalid activity", mutate: func(observation *Observation) { value := Activity("busy"); observation.SetActivity(&value) }},
		{name: "incomplete process", mutate: func(observation *Observation) { observation.SetProcess(&ProcessIdentity{PID: 42}) }},
		{name: "negative parent pid", mutate: func(observation *Observation) {
			observation.SetProcess(&ProcessIdentity{PID: 42, PPID: -1, StartIdentity: "boot:42"})
		}},
		{name: "negative tmux pid", mutate: func(observation *Observation) { observation.SetLocation(&Location{Kind: MultiplexerTmux, PanePID: -1}) }},
		{name: "negative catalog pid", mutate: func(observation *Observation) { observation.SetListing(&Listing{ProcessPID: -1}) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := base
			test.mutate(&observation)
			if err := observation.Validate(fixtureRules{}); !errors.Is(err, ErrInvalidObservation) {
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
	store := NewJournal(path, fixtureRules{})
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "session"}, Evidence: &Report{Event: "start"}}); err != nil {
		t.Fatal(err)
	}

	err := store.withSnapshot(t.Context(), func(snap *snapshot) error {
		id, session := onlyStoredSession(snap.Sessions)
		session.Liveness = Live{Activity: Activity("invalid"), Decision: nil}
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
	if len(sessions) != 1 || sessions[0].Activity() == nil {
		t.Fatalf("invalid mutation reached disk: %#v", sessions)
	}
}

func writeCorruptTestStore(t *testing.T, mutate func(*snapshot)) *Journal {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := NewJournal(path, fixtureRules{})
	_, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: time.Now().UTC(), Subject: ObservationIdentity{SessionID: "session"}, Evidence: &Report{Event: "start"}})
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
	session.setActivity(&activity)
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
	session.setPresence(PresenceLive)
	session.Liveness = Live{Activity: Activity("invalid"), Decision: nil}
	session.Process = &process
	session.PresenceChangedAt = nativeAt
	session.ActivityChangedAt = processAt
	session.Observations.Native.Lifecycle = &start
	session.Observations.Native.Presence = &live
	session.Observations.Native.Activity = &idle
	session.Observations.Native.Process = process
	session.Observations.Process = &ProcessObservation{Present: false, Process: process, ObservedAt: processAt}
	session.setDecision(&ActivityDecision{Authority: "process", Reason: "process_gone", Process: process, ObservedAt: processAt})
	snap.Sessions[id] = session
}

func TestScreenObservationDoesNotRegressNewerNativeActivity(t *testing.T) {
	t.Parallel()

	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	base := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 4242, PPID: 1, ProcessGroupID: 4242, StartIdentity: "boot:4242", Executable: "/usr/bin/codex", CWD: "/repo"}
	live := PresenceLive
	start := NativeLifecycleStart
	idle := ActivityIdle
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base, Subject: ObservationIdentity{SessionID: "screen-regression"}, Evidence: &Report{Event: "start", Lifecycle: &start, Claim: &live, Activity: &idle, Process: process}}); err != nil {
		t.Fatal(err)
	}
	nativeDecisionAt := base.Add(10 * time.Second)
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: nativeDecisionAt, Subject: ObservationIdentity{SessionID: "screen-regression"}, Evidence: &Report{Event: "hook", Activity: &idle, Process: process}}); err != nil {
		t.Fatal(err)
	}
	waiting := ActivityWaiting
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base.Add(5 * time.Second), Subject: ObservationIdentity{SessionID: "screen-regression"}, Evidence: (*Reading)(&ScreenObservation{Activity: waiting, Authority: "screen", Reason: "prompt", Process: *process})})
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityIdle {
		t.Fatalf("screen observation at T=5s regressed native decision at T=10s: activity = %v", session.Activity())
	}
	if session.Decision() == nil || !session.Decision().ObservedAt.Equal(nativeDecisionAt) {
		t.Fatalf("activity decision = %#v, want observed at %s", session.Decision(), nativeDecisionAt)
	}
	if session.Observations.Screen == nil || session.Observations.Screen.ObservedAt.IsZero() {
		t.Fatalf("older screen observation was not recorded: %#v", session.Observations.Screen)
	}
}

func TestAgentRestartWithProvisionalScanKeepsSingleLiveSession(t *testing.T) {
	t.Parallel()

	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	base := time.Now().UTC().Add(-time.Minute)
	oldProcess := &ProcessIdentity{PID: 100, PPID: 1, ProcessGroupID: 100, StartIdentity: "boot:100", Executable: "/usr/bin/codex", CWD: "/repo"}
	newProcess := &ProcessIdentity{PID: 200, PPID: 1, ProcessGroupID: 200, StartIdentity: "boot:200", Executable: "/usr/bin/codex", CWD: "/repo"}
	live := PresenceLive
	start := NativeLifecycleStart
	idle := ActivityIdle
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base, Subject: ObservationIdentity{SessionID: "restart"}, Evidence: &Report{Event: "start", Lifecycle: &start, Claim: &live, Activity: &idle, Process: oldProcess}}); err != nil {
		t.Fatal(err)
	}
	present := true
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base.Add(time.Second), Subject: ObservationIdentity{}, Evidence: &Sighting{Process: *newProcess, Present: present}}); err != nil {
		t.Fatal(err)
	}
	resume := NativeLifecycleResume
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: base.Add(2 * time.Second), Subject: ObservationIdentity{SessionID: "restart"}, Evidence: &Report{Event: "resume", Lifecycle: &resume, Claim: &live, Activity: &idle, Process: newProcess}}); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	liveTotal, liveWithIdentity := 0, 0
	for _, session := range sessions {
		if session.Presence() != PresenceLive {
			continue
		}
		liveTotal++
		if session.SessionID == "restart" {
			liveWithIdentity++
		}
	}
	if liveTotal != 1 || liveWithIdentity != 1 {
		t.Fatalf("live sessions = %d (session_id=restart: %d), want exactly one: %#v", liveTotal, liveWithIdentity, sessions)
	}
}

func TestStoreOlderProcessAbsenceDoesNotOverrideNewerNativePresence(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	proc := &ProcessIdentity{PID: 1234, StartIdentity: "start"}
	initial := Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "active"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning), Process: proc}}
	saved, err := s.Observe(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	fresh := Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "active"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning), Process: proc}}
	if _, err = s.Observe(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	// Delayed observer batch: process absence sampled before the latest native callback.
	absent := Observation{Harness: HarnessPi, At: now.Add(-5 * time.Second), Subject: ObservationIdentity{}, Evidence: &Sighting{Process: *proc, Present: false}}
	if _, err = s.Observe(ctx, absent); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Presence() == PresenceGone {
		t.Fatalf("native evidence at %s was overridden by older absence at %s", fresh.At, absent.At)
	}
}

func TestStoreMultiSessionHarnessDoesNotRetireSiblingSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	proc := &ProcessIdentity{PID: 1234, StartIdentity: "gateway-incarnation"}
	first := Observation{Harness: HarnessOpenClaw, At: now, Subject: ObservationIdentity{SessionID: "concurrent-A"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning), Process: proc}}
	saved, err := s.Observe(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	second := Observation{Harness: HarnessOpenClaw, At: now, Subject: ObservationIdentity{SessionID: "concurrent-B"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning), Process: proc}}
	if _, err = s.Observe(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Presence() != PresenceLive {
		t.Fatalf("session A became %q with reason=%q solely because session B reported the same host process", got.Presence(), got.Decision().Reason)
	}
}

func TestStoreSameMillisecondNativeEventsDoNotConflict(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	first := Observation{Harness: HarnessKilo, At: now, Subject: ObservationIdentity{SessionID: "kilo-session"}, Evidence: &Report{Reporter: Reporter{Integration: "kilo-plugin"}, Event: "session.status", Claim: new(PresenceLive), Activity: new(ActivityRunning), Attributes: map[string]string{"kilo_status": "busy"}}}
	saved, err := s.Observe(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := Observation{Harness: HarnessKilo, At: now, Subject: ObservationIdentity{SessionID: "kilo-session"}, Evidence: &Report{Reporter: Reporter{Integration: "kilo-plugin"}, Event: "session.status", Claim: new(PresenceLive), Activity: new(ActivityIdle), Attributes: map[string]string{"kilo_status": "idle"}}}
	_, err = s.Observe(ctx, second)
	got, getErr := s.Get(ctx, saved.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if errors.Is(err, ErrObservationConflict) {
		t.Fatalf("distinct sequential event in the same millisecond rejected: %v; activity remains %q", err, *got.Activity())
	}
	if err != nil {
		t.Fatal(err)
	}
}

func onlyStoredSession(sessions map[string]Session) (string, Session) {
	for id, session := range sessions {
		return id, session
	}
	return "", Session{}
}
