package registry

import (
	"errors"
	"fmt"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestStoreBackendsRemainEquivalentAcrossOperationSequences(t *testing.T) {
	t.Parallel()

	seeds := uint64(8)
	steps := 80
	if testing.Short() {
		seeds = 4
		steps = 40
	} else if os.Getenv("AHT_EXTENDED_PARITY") != "" {
		seeds = 32
		steps = 200
	}

	for seed := range seeds {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			t.Parallel()
			exerciseEquivalentStores(t, seed, steps)
		})
	}
}

func exerciseEquivalentStores(t *testing.T, seed uint64, steps int) {
	t.Helper()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	filePath := filepath.Join(t.TempDir(), "file.json")
	fileStore := NewJournal(filePath, fixtureRules{})
	// Reducer parity ignores owner-side tombstone expiry, which has its own tests.
	memoryStore, err := OpenMemoryStoreWithOptions(filepath.Join(t.TempDir(), "memory.json"), fixtureRules{}, MemoryStoreOptions{TombstoneTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	clock := base.Add(time.Hour)
	fileStore.setNowForTest(func() time.Time { return clock })
	memoryStore.setNowForTest(func() time.Time { return clock })

	generator := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // Reproducible property-test sequences are required.
	sequences := map[string]uint64{}
	for step := range steps {
		// Semantic variation: inject conflicting batch periodically
		if step%25 == 15 {
			injectParityConflictBatch(t, fileStore, memoryStore, base, step)
		}

		observation := generatedObservation(generator, sequences, base, step)
		fileSession, fileErr := fileStore.Observe(t.Context(), observation)
		memorySession, memoryErr := memoryStore.Observe(t.Context(), observation)
		if !sameStoreError(fileErr, memoryErr) {
			t.Fatalf("step %d observation %#v: file error %v, memory error %v", step, observation, fileErr, memoryErr)
		}
		if fileErr == nil {
			assertTerminalLifecycleOutcome(t, step, observation, fileSession, memorySession)
			assertEquivalent(t, fmt.Sprintf("step %d session", step), fileSession, memorySession)
		}

		fileSessions, fileStateErr := fileStore.List(t.Context(), Filter{})
		memorySessions, memoryStateErr := memoryStore.List(t.Context(), Filter{})
		if fileStateErr != nil || memoryStateErr != nil {
			t.Fatalf("step %d list errors: file %v, memory %v", step, fileStateErr, memoryStateErr)
		}
		assertEquivalent(t, fmt.Sprintf("step %d sessions", step), fileSessions, memorySessions)

		// Semantic variation: reopen FileStore mid-sequence to verify persistence
		if step == steps/2 {
			assertReopenedFileStore(t, filePath, clock, step, memorySessions)
		}
	}
}

func injectParityConflictBatch(t *testing.T, fileStore Store, memStore Store, base time.Time, step int) {
	t.Helper()
	running, idle := ActivityRunning, ActivityIdle
	conflictBatch := []Observation{
		{Harness: HarnessCodex, At: base.Add(time.Duration(step) * time.Millisecond), Subject: ObservationIdentity{SessionID: "conflict-item"}, Evidence: &Report{Activity: &running}},
		{Harness: HarnessCodex, At: base.Add(time.Duration(step) * time.Millisecond), Subject: ObservationIdentity{SessionID: "conflict-item"}, Evidence: &Report{Activity: &idle}},
	}
	_, fileBatchErr := fileStore.ObserveBatch(t.Context(), conflictBatch)
	_, memBatchErr := memStore.ObserveBatch(t.Context(), conflictBatch)
	if !sameStoreError(fileBatchErr, memBatchErr) {
		t.Fatalf("step %d conflict batch: file %v, mem %v", step, fileBatchErr, memBatchErr)
	}
}

func assertTerminalLifecycleOutcome(t *testing.T, step int, obs Observation, fileS Session, memS Session) {
	t.Helper()
	if obs.Report().Lifecycle == nil || *obs.Report().Lifecycle != NativeLifecycleEnd {
		return
	}
	if fileS.Presence() != PresenceGone || fileS.Activity() != nil {
		t.Fatalf("step %d file terminal observation resulted in presence %s, activity %v", step, fileS.Presence(), fileS.Activity())
	}
	if memS.Presence() != PresenceGone || memS.Activity() != nil {
		t.Fatalf("step %d memory terminal observation resulted in presence %s, activity %v", step, memS.Presence(), memS.Activity())
	}
}

func assertReopenedFileStore(t *testing.T, filePath string, clock time.Time, step int, memorySessions []Session) {
	t.Helper()
	reopenedFileStore := NewJournal(filePath, fixtureRules{})
	reopenedFileStore.setNowForTest(func() time.Time { return clock })
	reopenedSessions, err := reopenedFileStore.List(t.Context(), Filter{})
	if err != nil {
		t.Fatalf("reopened file store list error: %v", err)
	}
	assertEquivalent(t, fmt.Sprintf("step %d reopened sessions", step), reopenedSessions, memorySessions)
}

//nolint:cyclop // property generator exercises diverse observation variants
func generatedObservation(generator *rand.Rand, sequences map[string]uint64, base time.Time, step int) Observation {
	sessionID := fmt.Sprintf("session-%d", generator.IntN(8))
	activity := []Activity{ActivityIdle, ActivityRunning, ActivityWaiting}[generator.IntN(3)]
	harness := []Harness{HarnessClaude, HarnessCodex, HarnessOmp, HarnessPi}[generator.IntN(4)]
	observedAt := base.Add(time.Duration(step) * time.Millisecond)
	pid := 100 + generator.IntN(8)
	process := &ProcessIdentity{
		PID:            pid,
		PPID:           1,
		ProcessGroupID: pid,
		Foreground:     true,
		StartIdentity:  fmt.Sprintf("boot:%d", pid),
		Executable:     "/bin/agent",
	}

	// Semantic variation across observation sources and evidence types
	switch generator.IntN(6) {
	case 0:
		// Process presence evidence
		present := generator.IntN(2) == 0
		return Observation{Harness: harness, At: observedAt, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Sighting{Process: *process, Present: present}}
	case 1:
		// Screen state evidence
		return Observation{Harness: harness, At: observedAt, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: (*Reading)(&ScreenObservation{
			Activity:   activity,
			Authority:  "screen",
			Reason:     "screen_rule",
			Process:    *process,
			ObservedAt: observedAt,
		})}
	default:
		// Native evidence with varied sequence and lifecycle
		var seq *uint64
		switch generator.IntN(4) {
		case 0:
			// Replay current sequence
			if s, ok := sequences[sessionID]; ok && s > 0 {
				seq = &s
			}
		case 1:
			// Stale sequence
			if s, ok := sequences[sessionID]; ok && s > 1 {
				stale := s - 1
				seq = &stale
			}
		}
		if seq == nil {
			s := sequences[sessionID] + 1
			sequences[sessionID] = s
			seq = &s
		}

		var lifecycle *NativeLifecycle
		switch generator.IntN(5) {
		case 0:
			l := NativeLifecycleStart
			lifecycle = &l
		case 1:
			l := NativeLifecycleResume
			lifecycle = &l
		case 2:
			l := NativeLifecycleEnd
			lifecycle = &l
		}
		var activityPtr *Activity
		if lifecycle == nil || *lifecycle != NativeLifecycleEnd {
			activityPtr = &activity
		}

		return Observation{Harness: harness, At: observedAt, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Report{Reporter: Reporter{Integration: "parity-test", Sequence: seq}, Event: []string{"session_start", "agent_start", "agent_end", "permission_request"}[generator.IntN(4)], Lifecycle: lifecycle, Activity: activityPtr, Process: process}}
	}
}

func TestStoreBackendsRemainEquivalentOnTerminalLifecycleSequence(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fileStore := NewJournal(filepath.Join(t.TempDir(), "file.json"), fixtureRules{})
	memStore, err := OpenMemoryStoreWithOptions(filepath.Join(t.TempDir(), "memory.json"), fixtureRules{}, MemoryStoreOptions{TombstoneTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	clock := base.Add(time.Hour)
	fileStore.setNowForTest(func() time.Time { return clock })
	memStore.setNowForTest(func() time.Time { return clock })
	sessionID := "terminal-parity-session"
	process := &ProcessIdentity{PID: 100, StartIdentity: "boot:100", Executable: "test"}

	testParityStartPhase(t, fileStore, memStore, sessionID, process, base)
	testParityInvalidEndPhase(t, fileStore, memStore, sessionID, process, base.Add(time.Second))
	testParityValidEndPhase(t, fileStore, memStore, sessionID, process, base.Add(2*time.Second))
	testParityProcessPresencePhase(t, fileStore, memStore, sessionID, process, base.Add(3*time.Second))
	testParityResumePhase(t, fileStore, memStore, sessionID, process, base.Add(4*time.Second))
}

func testParityStartPhase(t *testing.T, fileStore Store, memStore Store, sessionID string, process *ProcessIdentity, at time.Time) {
	t.Helper()
	testParityNativeLifecyclePhase(t, fileStore, memStore, sessionID, process, at, NativeLifecycleStart, "session_start")
}

func testParityNativeLifecyclePhase(
	t *testing.T,
	fileStore Store,
	memStore Store,
	sessionID string,
	process *ProcessIdentity,
	at time.Time,
	lifecycle NativeLifecycle,
	expectedEvent string,
) {
	t.Helper()
	running := ActivityRunning
	live := PresenceLive
	obs := Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Report{Event: expectedEvent, Lifecycle: &lifecycle, Claim: &live, Activity: &running, Process: process}}
	fileS, fErr := fileStore.Observe(t.Context(), obs)
	memS, mErr := memStore.Observe(t.Context(), obs)
	if fErr != nil || mErr != nil {
		t.Fatalf("%s errors: file %v, mem %v", lifecycle, fErr, mErr)
	}
	if fileS.Presence() != PresenceLive || fileS.Activity() == nil || *fileS.Activity() != ActivityRunning {
		t.Fatalf("%s session state = presence:%s activity:%v, want live/running", lifecycle, fileS.Presence(), fileS.Activity())
	}
	assertEquivalent(t, string(lifecycle)+" session", fileS, memS)
}

func testParityInvalidEndPhase(t *testing.T, fileStore Store, memStore Store, sessionID string, process *ProcessIdentity, at time.Time) {
	t.Helper()
	running := ActivityRunning
	end := NativeLifecycleEnd
	invalidEnd := Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Report{Event: "session_end", Lifecycle: &end, Activity: &running, Process: process}}
	_, fErr := fileStore.Observe(t.Context(), invalidEnd)
	_, mErr := memStore.Observe(t.Context(), invalidEnd)
	if !errors.Is(fErr, ErrInvalidObservation) || !errors.Is(mErr, ErrInvalidObservation) {
		t.Fatalf("invalid end errors: file %v, mem %v", fErr, mErr)
	}
}

func testParityValidEndPhase(t *testing.T, fileStore Store, memStore Store, sessionID string, process *ProcessIdentity, at time.Time) {
	t.Helper()
	end := NativeLifecycleEnd
	validEnd := Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Report{Event: "session_end", Lifecycle: &end, Activity: nil, Process: process}}
	fileS, fErr := fileStore.Observe(t.Context(), validEnd)
	memS, mErr := memStore.Observe(t.Context(), validEnd)
	if fErr != nil || mErr != nil {
		t.Fatalf("valid end errors: file %v, mem %v", fErr, mErr)
	}
	if fileS.Presence() != PresenceGone || fileS.Activity() != nil {
		t.Fatalf("valid end file session state = presence:%s activity:%v, want gone/nil", fileS.Presence(), fileS.Activity())
	}
	if memS.Presence() != PresenceGone || memS.Activity() != nil {
		t.Fatalf("valid end mem session state = presence:%s activity:%v, want gone/nil", memS.Presence(), memS.Activity())
	}
	assertEquivalent(t, "valid end session", fileS, memS)
}

func testParityProcessPresencePhase(t *testing.T, fileStore Store, memStore Store, sessionID string, process *ProcessIdentity, at time.Time) {
	t.Helper()
	present := true
	procObs := Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: sessionID}, Evidence: &Sighting{Process: *process, Present: present}}
	fileS, fErr := fileStore.Observe(t.Context(), procObs)
	memS, mErr := memStore.Observe(t.Context(), procObs)
	if fErr != nil || mErr != nil {
		t.Fatalf("process presence errors: file %v, mem %v", fErr, mErr)
	}
	if fileS.Presence() != PresenceGone || fileS.Activity() != nil {
		t.Fatalf("retained end file session state = presence:%s activity:%v, want gone/nil", fileS.Presence(), fileS.Activity())
	}
	if memS.Presence() != PresenceGone || memS.Activity() != nil {
		t.Fatalf("retained end mem session state = presence:%s activity:%v, want gone/nil", memS.Presence(), memS.Activity())
	}
	assertEquivalent(t, "retained end blocking", fileS, memS)
}

func testParityResumePhase(t *testing.T, fileStore Store, memStore Store, sessionID string, process *ProcessIdentity, at time.Time) {
	t.Helper()
	testParityNativeLifecyclePhase(t, fileStore, memStore, sessionID, process, at, NativeLifecycleResume, "session_resume")
}

func sameStoreError(left error, right error) bool {
	for _, target := range []error{ErrSessionNotFound, ErrHarnessRequired, ErrObservationIdentity, ErrObservationConflict, ErrCorruptStore} {
		if errors.Is(left, target) != errors.Is(right, target) {
			return false
		}
	}
	return (left == nil) == (right == nil)
}

func assertEquivalent(t *testing.T, label string, left any, right any) {
	t.Helper()

	opt := cmp.Comparer(func(x, y ProcessIdentity) bool {
		return x == y
	})
	if diff := cmp.Diff(left, right, opt); diff != "" {
		t.Fatalf("%s mismatch (-file +memory):\n%s", label, diff)
	}
}
