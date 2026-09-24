package registry

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

//nolint:cyclop // One state-transition scenario verifies every revision invariant.
func TestMemoryStorePublishesOnlyEffectiveStateChanges(t *testing.T) {
	t.Parallel()

	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store.setNowForTest(func() time.Time { return base })

	initial, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || len(initial.Sessions) != 0 {
		t.Fatalf("initial state = %#v, want empty revision 1", initial)
	}

	running := ActivityRunning
	observation := Observation{Harness: HarnessOmp, At: base, Subject: ObservationIdentity{SessionID: "live"}, Evidence: &Report{Reporter: Reporter{Integration: "omp-extension"}, Event: "agent_start", Activity: &running}}
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	changed, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision != 2 || len(changed.Sessions) != 1 {
		t.Fatalf("changed state = %#v, want one session at revision 2", changed)
	}

	observation.At = base.Add(time.Second)
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	heartbeat, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Revision != changed.Revision {
		t.Fatalf("heartbeat revision = %d, want unchanged %d", heartbeat.Revision, changed.Revision)
	}

	idle := ActivityIdle
	observation.SetActivity(&idle)
	observation.Report().Event = "agent_end"
	observation.At = base.Add(2 * time.Second)
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	settled, err := store.WaitForRevision(t.Context(), changed.Revision, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if settled.Revision != 3 || settled.Sessions[0].Activity() == nil || *settled.Sessions[0].Activity() != ActivityIdle {
		t.Fatalf("settled state = %#v, want idle revision 3", settled)
	}
}

func TestSequencedNativeReportsRejectStaleAndUnsequencedUpdates(t *testing.T) {
	t.Parallel()

	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store.setNowForTest(func() time.Time { return base.Add(time.Minute) })
	reporter := Reporter{Integration: "omp-extension"}
	running := ActivityRunning
	sequence := uint64(20)
	reporter.Sequence = &sequence
	observation := Observation{Harness: HarnessOmp, At: base.Add(10 * time.Second), Subject: ObservationIdentity{SessionID: "sequenced"}, Evidence: &Report{Reporter: reporter, Event: "agent_start", Activity: &running}}
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	idle := ActivityIdle
	staleSequence := uint64(19)
	observation.SetActivity(&idle)
	observation.Report().Reporter.Sequence = &staleSequence
	observation.At = base.Add(20 * time.Second)
	if _, err := store.Observe(t.Context(), observation); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stale sequence error = %v, want observation conflict", err)
	}

	observation.Report().Reporter.Sequence = nil
	observation.At = base.Add(30 * time.Second)
	if _, err := store.Observe(t.Context(), observation); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("unsequenced report error = %v, want observation conflict", err)
	}

	newSequence := uint64(21)
	observation.Report().Reporter.Sequence = &newSequence
	observation.At = base
	session, err := store.Observe(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityIdle {
		t.Fatalf("sequenced clock-regressed session = %#v, want idle", session)
	}
	if session.Observations.Native == nil || session.Observations.Native.Reporter.Sequence == nil || *session.Observations.Native.Reporter.Sequence != newSequence {
		t.Fatalf("native sequence = %#v, want %d", session.Observations.Native, newSequence)
	}
}

func BenchmarkRegistryObserveWith65Sessions(b *testing.B) {
	b.Run("file", func(b *testing.B) {
		benchmarkRegistryObserve(b, NewJournal(filepath.Join(b.TempDir(), "state.json"), fixtureRules{}))
	})
	b.Run("memory", func(b *testing.B) {
		store, err := OpenMemoryStore(filepath.Join(b.TempDir(), "state.json"), fixtureRules{})
		if err != nil {
			b.Fatal(err)
		}
		benchmarkRegistryObserve(b, store)
	})
}

func benchmarkRegistryObserve(b *testing.B, store Store) {
	b.Helper()

	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	idle := ActivityIdle
	seed := make([]Observation, 65)
	for index := range seed {
		seed[index] = Observation{Harness: HarnessOmp, At: base.Add(time.Duration(index) * time.Nanosecond), Subject: ObservationIdentity{SessionID: "session-" + strconv.Itoa(index)}, Evidence: &Report{Reporter: Reporter{Integration: "omp-extension"}, Event: "session_start", Activity: &idle}}
	}
	if _, err := store.ObserveBatch(b.Context(), seed); err != nil {
		b.Fatal(err)
	}

	running := ActivityRunning
	observation := seed[0]
	observation.SetActivity(&running)
	observation.Report().Event = "agent_start"
	observation.At = base.Add(time.Second)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		observation.At = observation.At.Add(time.Nanosecond)
		if _, err := store.Observe(b.Context(), observation); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMemoryStoreResetClearsLiveState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	saved, err := s.Observe(ctx, Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "reset-me"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning)}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	disk := NewJournal(path, fixtureRules{})
	if _, err = disk.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	live, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = s.Observe(ctx, Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "unrelated"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning)}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	_, restoredErr := disk.Get(ctx, saved.ID)
	if len(live) != 0 || restoredErr == nil {
		t.Fatalf("reset left %d live records; deleted record restored on flush=%v", len(live), restoredErr == nil)
	}
}

func TestMemoryStoreFlushPreservesExternalFallbackWrites(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	disk := NewJournal(path, fixtureRules{})
	disk.setNowForTest(func() time.Time { return now })
	saved, err := disk.Observe(ctx, Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "arrived-before-socket"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Observe(ctx, Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "broker-update"}, Evidence: &Report{Event: "agent_start", Claim: new(PresenceLive), Activity: new(ActivityRunning)}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = disk.Get(ctx, saved.ID); errors.Is(err, ErrSessionNotFound) {
		t.Fatal("successful fallback observation overwritten by broker loaded snapshot")
	} else if err != nil {
		t.Fatal(err)
	}
}
