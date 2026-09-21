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

	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"))
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
	observation := Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessOmp,
		Identity:    ObservationIdentity{SessionID: "live"},
		Activity:    &running,
		NativeEvent: "agent_start",
		Attributes:  map[string]string{"aht_integration": "omp-extension"},
		ObservedAt:  base,
	}
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

	observation.ObservedAt = base.Add(time.Second)
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
	observation.Activity = &idle
	observation.NativeEvent = "agent_end"
	observation.ObservedAt = base.Add(2 * time.Second)
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	settled, err := store.WaitForRevision(t.Context(), changed.Revision, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if settled.Revision != 3 || settled.Sessions[0].Activity == nil || *settled.Sessions[0].Activity != ActivityIdle {
		t.Fatalf("settled state = %#v, want idle revision 3", settled)
	}
}

func TestSequencedNativeReportsRejectStaleAndUnsequencedUpdates(t *testing.T) {
	t.Parallel()

	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	store.setNowForTest(func() time.Time { return base.Add(time.Minute) })
	reporter := map[string]string{"aht_integration": "omp-extension"}
	running := ActivityRunning
	sequence := uint64(20)
	observation := Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessOmp,
		Identity:    ObservationIdentity{SessionID: "sequenced"},
		Activity:    &running,
		Sequence:    &sequence,
		NativeEvent: "agent_start",
		Attributes:  reporter,
		ObservedAt:  base.Add(10 * time.Second),
	}
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	idle := ActivityIdle
	staleSequence := uint64(19)
	observation.Activity = &idle
	observation.Sequence = &staleSequence
	observation.ObservedAt = base.Add(20 * time.Second)
	if _, err := store.Observe(t.Context(), observation); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stale sequence error = %v, want observation conflict", err)
	}

	observation.Sequence = nil
	observation.ObservedAt = base.Add(30 * time.Second)
	if _, err := store.Observe(t.Context(), observation); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("unsequenced report error = %v, want observation conflict", err)
	}

	newSequence := uint64(21)
	observation.Sequence = &newSequence
	observation.ObservedAt = base
	session, err := store.Observe(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity == nil || *session.Activity != ActivityIdle {
		t.Fatalf("sequenced clock-regressed session = %#v, want idle", session)
	}
	if session.Observations.Native == nil || session.Observations.Native.Sequence == nil || *session.Observations.Native.Sequence != newSequence {
		t.Fatalf("native sequence = %#v, want %d", session.Observations.Native, newSequence)
	}
}

func BenchmarkRegistryObserveWith65Sessions(b *testing.B) {
	b.Run("file", func(b *testing.B) {
		benchmarkRegistryObserve(b, NewFileStore(filepath.Join(b.TempDir(), "state.json")))
	})
	b.Run("memory", func(b *testing.B) {
		store, err := OpenMemoryStore(filepath.Join(b.TempDir(), "state.json"))
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
		seed[index] = Observation{
			Source:      ObservationSourceNative,
			Evidence:    ObservationEvidenceNativeEvent,
			Harness:     HarnessOmp,
			Identity:    ObservationIdentity{SessionID: "session-" + strconv.Itoa(index)},
			Activity:    &idle,
			NativeEvent: "session_start",
			Attributes:  map[string]string{"aht_integration": "omp-extension"},
			ObservedAt:  base.Add(time.Duration(index) * time.Nanosecond),
		}
	}
	if _, err := store.ObserveBatch(b.Context(), seed); err != nil {
		b.Fatal(err)
	}

	running := ActivityRunning
	observation := seed[0]
	observation.Activity = &running
	observation.NativeEvent = "agent_start"
	observation.ObservedAt = base.Add(time.Second)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		observation.ObservedAt = observation.ObservedAt.Add(time.Nanosecond)
		if _, err := store.Observe(b.Context(), observation); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMemoryStoreResetClearsLiveState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := OpenMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	saved, err := s.Observe(ctx, Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessPi,
		Identity:    ObservationIdentity{SessionID: "reset-me"},
		Presence:    new(PresenceLive),
		Activity:    new(ActivityRunning),
		NativeEvent: "agent_start",
		ObservedAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	disk := NewFileStore(path)
	if _, err = disk.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	live, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = s.Observe(ctx, Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessPi,
		Identity:    ObservationIdentity{SessionID: "unrelated"},
		Presence:    new(PresenceLive),
		Activity:    new(ActivityRunning),
		NativeEvent: "agent_start",
		ObservedAt:  now,
	}); err != nil {
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
	s, err := OpenMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	s.setNowForTest(func() time.Time { return now })
	disk := NewFileStore(path)
	disk.setNowForTest(func() time.Time { return now })
	saved, err := disk.Observe(ctx, Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessPi,
		Identity:    ObservationIdentity{SessionID: "arrived-before-socket"},
		Presence:    new(PresenceLive),
		Activity:    new(ActivityRunning),
		NativeEvent: "agent_start",
		ObservedAt:  now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Observe(ctx, Observation{
		Source:      ObservationSourceNative,
		Evidence:    ObservationEvidenceNativeEvent,
		Harness:     HarnessPi,
		Identity:    ObservationIdentity{SessionID: "broker-update"},
		Presence:    new(PresenceLive),
		Activity:    new(ActivityRunning),
		NativeEvent: "agent_start",
		ObservedAt:  now,
	}); err != nil {
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
