package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalReadersSeeUnflushedBrokerAndFallbackWrites(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	})
	observation := Observation{Harness: HarnessPi, At: time.Time{}, Subject: ObservationIdentity{SessionID: "queued"}, Evidence: &Report{Activity: new(ActivityRunning)}}
	saved, err := memory.Observe(t.Context(), observation)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewFileStore(path, fixtureRules{})
	got, err := reader.Get(t.Context(), saved.ID)
	assertJournalActivity(t, got, err, ActivityRunning)
	observation.SetActivity(new(ActivityWaiting))
	if _, err := NewJournal(path, fixtureRules{}).Append(t.Context(), []Observation{observation}); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Get(t.Context(), saved.ID)
	assertJournalActivity(t, got, err, ActivityWaiting)
	got, err = memory.Get(t.Context(), saved.ID)
	if err != nil || got.Activity() == nil || *got.Activity() != ActivityWaiting {
		t.Fatalf("drain fallback journal: %+v, %v", got, err)
	}
}

func TestJournalSnapshotSequencePreventsReplayAfterCrash(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = memory.Observe(t.Context(), Observation{Harness: HarnessPi, At: time.Time{}, Subject: ObservationIdentity{SessionID: "gone"}, Evidence: &Report{Lifecycle: new(NativeLifecycleEnd)}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path + ".journal.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.GC(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if err := memory.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeJournalForTest(t, path, data)
	sessions, err := NewFileStore(path, fixtureRules{}).List(t.Context(), Filter{})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("replayed checkpointed command: %+v, %v", sessions, err)
	}
}

func TestMemoryStoreOwnershipIsExclusiveAndReleased(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenMemoryStore(path, fixtureRules{}); !errors.Is(err, ErrStoreOwned) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("second owner: %v", err)
	}
	if err := memory.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJournalHeartbeatDoesNotAdvanceConsumerRevision(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	observation := Observation{Harness: HarnessPi, At: now, Subject: ObservationIdentity{SessionID: "heartbeat"}, Evidence: &Report{Activity: new(ActivityRunning)}}
	if _, err := memory.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}
	before, err := memory.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	observation.At = now.Add(time.Second)
	if _, err := NewJournal(path, fixtureRules{}).Append(t.Context(), []Observation{observation}); err != nil {
		t.Fatal(err)
	}
	after, err := memory.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("heartbeat revision = %d, want %d", after.Revision, before.Revision)
	}
}

func TestJournalDiscardsUncommittedPartialTail(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	memory, err := OpenMemoryStore(path, fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memory.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(path+".journal.jsonl", []byte(`{"sequence":`), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, err := NewJournal(path, fixtureRules{}).Observe(t.Context(), Observation{Harness: HarnessPi, At: time.Time{}, Subject: ObservationIdentity{SessionID: "complete"}, Evidence: &Report{Activity: new(ActivityRunning)}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := memory.Get(t.Context(), saved.ID)
	if err != nil || got.Activity() == nil || *got.Activity() != ActivityRunning {
		t.Fatalf("complete report after partial tail: %+v, %v", got, err)
	}
}

func assertJournalActivity(t *testing.T, session Session, err error, want Activity) {
	t.Helper()
	if err != nil || session.Activity() == nil || *session.Activity() != want {
		t.Fatalf("journal activity = %+v, %v; want %s", session, err, want)
	}
}

func writeJournalForTest(t *testing.T, path string, data []byte) {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := root.WriteFile("state.json.journal.jsonl", data, 0o600); err != nil {
		t.Fatal(err)
	}
}

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
