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

	"github.com/zigai/aht/v2/internal/registrycheck/core"
)

func TestStoreBackendsRemainEquivalentAcrossOperationSequences(t *testing.T) {
	t.Parallel()

	seeds := uint64(8)
	steps := 80
	coverage := core.NewCoverage()
	t.Cleanup(func() {
		if missing := coverage.Missing("stale sequence", "conflicting batch", "native end", "waiting activity", "screen evidence", "reopen", "gc", "reset", "same-process replaced sighting blocked", "different-process replaced sighting revived"); len(missing) != 0 {
			t.Errorf("parity coverage missing: %v", missing)
		}
	})
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
			exerciseEquivalentStores(t, seed, steps, coverage)
		})
	}
}

//nolint:gocognit,cyclop // A parity step intentionally checks every store operation and its resulting state.
func exerciseEquivalentStores(t *testing.T, seed uint64, steps int, coverage *core.Coverage) {
	t.Helper()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	filePath := filepath.Join(t.TempDir(), "file.json")
	fileStore := NewJournal(filePath, fixtureRules{})
	// Reducer parity ignores owner-side tombstone expiry, which has its own tests.
	memoryStore, err := OpenMemoryStoreWithOptions(filepath.Join(t.TempDir(), "memory.json"), fixtureRules{}, MemoryStoreOptions{TombstoneTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := memoryStore.Close(); err != nil {
			t.Error(err)
		}
	})
	clock := base.Add(time.Hour)
	fileStore.setNowForTest(func() time.Time { return clock })
	memoryStore.setNowForTest(func() time.Time { return clock })

	generator := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // Reproducible property-test sequences are required.
	replacementStep := generator.IntN(steps)
	sequences := map[string]uint64{}
	for step := range steps {
		if step == replacementStep {
			exerciseReplacedSightingPair(t, fileStore, memoryStore, base, seed, step, coverage)
		}
		if step%25 == 15 {
			injectParityConflictBatch(t, fileStore, memoryStore, base, step)
			coverage.Hit("conflicting batch")
		}
		if step > 0 && generator.IntN(16) == 0 {
			assertReopenedFileStore(t, filePath, clock, step, mustListParity(t, memoryStore))
			coverage.Hit("reopen")
		}
		if step > 0 && generator.IntN(20) == 0 {
			deleteAfter := time.Duration(generator.IntN(3)-1) * time.Hour
			before := mustListParity(t, fileStore)
			fileGC, fileErr := fileStore.GC(t.Context(), deleteAfter)
			memoryGC, memoryErr := memoryStore.GC(t.Context(), deleteAfter)
			if !sameStoreError(fileErr, memoryErr) {
				t.Fatalf("seed %d step %d gc errors: %v, %v", seed, step, fileErr, memoryErr)
			}
			if fileErr == nil {
				assertEquivalent(t, fmt.Sprintf("seed %d step %d gc", seed, step), fileGC, memoryGC)
				assertGCOnlyExpiredGone(t, before, mustListParity(t, fileStore), clock, deleteAfter)
			}
			coverage.Hit("gc")
		}
		if step > 0 && generator.IntN(35) == 0 {
			fileReset, fileErr := fileStore.Reset(t.Context())
			memoryReset, memoryErr := memoryStore.Reset(t.Context())
			if !sameStoreError(fileErr, memoryErr) {
				t.Fatalf("seed %d step %d reset errors: %v, %v", seed, step, fileErr, memoryErr)
			}
			if fileErr == nil {
				assertEquivalent(t, fmt.Sprintf("seed %d step %d reset", seed, step), fileReset, memoryReset)
			}
			coverage.Hit("reset")
		}

		observation := generatedObservation(generator, sequences, base, step)
		fileSession, fileErr := fileStore.Observe(t.Context(), observation)
		memorySession, memoryErr := memoryStore.Observe(t.Context(), observation)
		if !sameStoreError(fileErr, memoryErr) {
			t.Fatalf("seed %d step %d observation %#v: file error %v, memory error %v", seed, step, observation, fileErr, memoryErr)
		}
		if errors.Is(fileErr, ErrObservationConflict) && rejectedStaleSequence(t, fileStore, observation) {
			coverage.Hit("stale sequence")
		}
		if fileErr == nil {
			assertTerminalLifecycleOutcome(t, step, observation, fileSession, memorySession)
			assertEquivalent(t, fmt.Sprintf("step %d session", step), fileSession, memorySession)
			if lifecycle := observation.Report().Lifecycle; observation.Kind() == "report" && lifecycle != nil && *lifecycle == NativeLifecycleEnd {
				coverage.Hit("native end")
			}
			if activity := fileSession.Activity(); activity != nil && *activity == ActivityWaiting {
				coverage.Hit("waiting activity")
			}
			if decision := fileSession.Decision(); observation.Kind() == "reading" && decision != nil && decision.Authority == AuthorityScreen {
				coverage.Hit("screen evidence")
			}
		}

		fileSessions, fileStateErr := fileStore.List(t.Context(), Filter{})
		memorySessions, memoryStateErr := memoryStore.List(t.Context(), Filter{})
		if fileStateErr != nil || memoryStateErr != nil {
			t.Fatalf("step %d list errors: file %v, memory %v", step, fileStateErr, memoryStateErr)
		}
		assertEquivalent(t, fmt.Sprintf("step %d sessions", step), fileSessions, memorySessions)
		if err := checkInvariantSessions(fileSessions); err != nil {
			t.Fatalf("seed %d step %d file invariants: %v", seed, step, err)
		}
		if err := checkInvariantSessions(memorySessions); err != nil {
			t.Fatalf("seed %d step %d memory invariants: %v", seed, step, err)
		}
		assertExclusiveProcesses(t, fileSessions)
	}
}

//nolint:gocognit,cyclop // The fixture checks both stores and both replacement sighting outcomes after every observation.
func exerciseReplacedSightingPair(t *testing.T, fileStore, memoryStore Store, base time.Time, seed uint64, step int, coverage *core.Coverage) {
	t.Helper()
	pid := 10000 + step
	process := ProcessIdentity{PID: pid, StartIdentity: fmt.Sprintf("replacement:%d", pid), Executable: "agent"}
	sameIdentity := process
	sameIdentity.Executable = "other-agent-path"
	newProcess := ProcessIdentity{PID: pid + 500000, StartIdentity: fmt.Sprintf("replacement:%d", pid+500000), Executable: "agent"}
	oldID := fmt.Sprintf("replacement-%d-%d-old", seed, step)
	newID := fmt.Sprintf("replacement-%d-%d-new", seed, step)
	at := base.Add(time.Duration(step) * time.Millisecond)
	live, running := PresenceLive, ActivityRunning
	observations := []Observation{
		{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: oldID}, Evidence: &Report{Event: "start", Claim: &live, Activity: &running, Process: &process}},
		{Harness: HarnessPi, At: at.Add(time.Nanosecond), Subject: ObservationIdentity{SessionID: newID}, Evidence: &Report{Event: "start", Claim: &live, Activity: &running, Process: &process}},
		{Harness: HarnessPi, At: at.Add(2 * time.Nanosecond), Subject: ObservationIdentity{SessionID: oldID}, Evidence: &Sighting{Process: sameIdentity, Present: true}},
		{Harness: HarnessPi, At: at.Add(3 * time.Nanosecond), Subject: ObservationIdentity{SessionID: oldID}, Evidence: &Sighting{Process: newProcess, Present: true}},
	}
	for index, observation := range observations {
		fileSession, fileErr := fileStore.Observe(t.Context(), observation)
		memorySession, memoryErr := memoryStore.Observe(t.Context(), observation)
		if !sameStoreError(fileErr, memoryErr) || fileErr != nil {
			t.Fatalf("replacement seed %d step %d operation %d errors: file %v, memory %v", seed, step, index, fileErr, memoryErr)
		}
		assertEquivalent(t, fmt.Sprintf("replacement seed %d step %d operation %d", seed, step, index), fileSession, memorySession)
		fileSessions := mustListParity(t, fileStore)
		assertEquivalent(t, "replacement sessions", fileSessions, mustListParity(t, memoryStore))
		if err := checkInvariantSessions(fileSessions); err != nil {
			t.Fatal(err)
		}
		assertExclusiveProcesses(t, fileSessions)
		if index == 2 && fileSession.Presence() != PresenceGone {
			t.Fatalf("same-process sighting revived replaced session %q", oldID)
		}
		if index == 3 && (fileSession.Presence() != PresenceLive || fileSession.Process == nil || !fileSession.Process.Equal(newProcess)) {
			t.Fatalf("different-process sighting did not revive session %q", oldID)
		}
		if coverage != nil {
			if index == 2 {
				coverage.Hit("same-process replaced sighting blocked")
			}
			if index == 3 {
				coverage.Hit("different-process replaced sighting revived")
			}
		}
	}
}

func mustListParity(t *testing.T, store Store) []Session {
	t.Helper()
	sessions, err := store.List(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return sessions
}

func rejectedStaleSequence(t *testing.T, store Store, observation Observation) bool {
	t.Helper()
	if observation.Kind() != "report" || observation.Report().Reporter.Sequence == nil {
		return false
	}
	for _, session := range mustListParity(t, store) {
		native := session.Observations.Native
		if session.Harness == observation.Harness && session.SessionID == observation.Subject.SessionID && native != nil &&
			native.Reporter.Integration == observation.Report().Reporter.Integration && native.Reporter.Sequence != nil &&
			*observation.Report().Reporter.Sequence <= *native.Reporter.Sequence {
			return true
		}
	}
	return false
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
