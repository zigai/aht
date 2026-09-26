package registry

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestFallbackReportSurvivesNewerMemoryHeartbeat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.json")
	durable := NewJournal(path, fixtureRules{})
	// Younger than the broker's tombstone TTL, so only the fallback GC removes it.
	base := time.Now().UTC().Add(-5 * time.Minute)
	running, waiting := ActivityRunning, ActivityWaiting
	process := ProcessIdentity{PID: 123, StartIdentity: "boot:123"}
	report := Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "fallback"}, Evidence: &Report{Activity: &running, Process: &process}}
	durable.setNowForTest(func() time.Time { return base })
	session, err := durable.Observe(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	memory.setNowForTest(func() time.Time { return base.Add(2 * time.Second) })
	present := true
	_, err = memory.Observe(t.Context(), Observation{Harness: HarnessPi, At: base.Add(2 * time.Second), Subject: ObservationIdentity{}, Evidence: &Sighting{Process: process, Present: present}})
	if err != nil {
		t.Fatal(err)
	}
	durable.setNowForTest(func() time.Time { return base.Add(time.Second) })
	report.SetActivity(&waiting)
	report.At = base.Add(time.Second)
	if _, err := durable.Observe(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	got, err := memory.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Activity() == nil || *got.Activity() != waiting {
		t.Fatalf("fallback report lost behind newer process heartbeat: activity = %v, want waiting", got.Activity())
	}
	if got.Observations.Process == nil || !got.Observations.Process.ObservedAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("newer process evidence lost: %+v", got.Observations.Process)
	}
}

func TestFallbackGCCannotBeResurrectedByMemoryFlush(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.json")
	durable := NewJournal(path, fixtureRules{})
	// Younger than the broker's tombstone TTL, so only the fallback GC removes it.
	base := time.Now().UTC().Add(-5 * time.Minute)
	durable.setNowForTest(func() time.Time { return base })
	end, idle := NativeLifecycleEnd, ActivityIdle
	ended, err := durable.Observe(t.Context(), Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "expired"}, Evidence: &Report{Lifecycle: &end}})
	if err != nil {
		t.Fatal(err)
	}
	live, err := durable.Observe(t.Context(), Observation{Harness: HarnessPi, At: base, Subject: ObservationIdentity{SessionID: "retained"}, Evidence: &Report{Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	durable.setNowForTest(func() time.Time { return base.Add(time.Hour) })
	result, err := durable.GC(t.Context(), time.Minute)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("GC = %+v, %v", result, err)
	}
	if err := memory.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := durable.Get(t.Context(), ended.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GC tombstone resurrected by broker flush: %v", err)
	}
	if _, err := memory.Get(t.Context(), ended.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GC tombstone retained by broker: %v", err)
	}
	if _, err := durable.Get(t.Context(), live.ID); err != nil {
		t.Fatalf("retained live session: %v", err)
	}
}
