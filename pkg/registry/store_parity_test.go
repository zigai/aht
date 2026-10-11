package registry

import (
	"context"
	"errors"
	"fmt"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/zigai/aht/v2/internal/registrycheck/core"
)

type registryModelValue struct {
	Presence       Presence
	Activity       Activity
	Process        ProcessIdentity
	CWD            string
	Sequence       uint64
	Event          string
	NativeActivity Activity
	NativeEnded    bool
}

type registryModelSession struct {
	Value    registryModelValue
	GoneAt   time.Time
	NativeAt time.Time
}

type registryModelCommand struct {
	ID       string
	Kind     string
	Activity Activity
	Process  ProcessIdentity
	Sequence uint64
	At       time.Time
}

type registryStateMachine struct {
	store interface {
		Store
		Reset(ctx context.Context) (ResetResult, error)
	}
	memory      *MemoryStore
	path        string
	backend     string
	clock       time.Time
	ttl         time.Duration
	names       []string
	generations map[string]int
	sessions    map[string]registryModelSession
	handles     map[string]string
}

func TestStoreBackendsMatchIndependentStateModel(t *testing.T) {
	t.Parallel()
	for _, backend := range []string{"journal", "memory"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			rapid.Check(t, func(property *rapid.T) {
				machine := &registryStateMachine{
					path: filepath.Join(t.TempDir(), "state.json"), backend: backend,
					clock:       time.Now().UTC().Add(time.Hour),
					ttl:         time.Duration(rapid.IntRange(1, 10).Draw(property, "tombstone minutes")) * time.Minute,
					generations: map[string]int{}, sessions: map[string]registryModelSession{}, handles: map[string]string{},
				}
				for index := range rapid.IntRange(2, 5).Draw(property, "native identities") {
					machine.names = append(machine.names, "native-"+strconv.Itoa(index)+"-"+rapid.StringMatching(`[[:alnum:]_-]+`).Draw(property, "native name"))
				}
				machine.open(property)
				property.Cleanup(func() {
					if machine.memory != nil {
						if err := machine.memory.Close(); err != nil {
							t.Error(err)
						}
					}
				})
				actions := map[string]func(*rapid.T){
					"":              machine.check,
					"screen":        machine.screen,
					"process-gone":  machine.processGone,
					"invalid-batch": machine.invalidBatch,
					"batch":         machine.batch,
					"gc":            machine.gc,
					"advance":       machine.advance,
					"reset":         machine.reset,
					"reload":        machine.reload,
				}
				for _, kind := range []string{"start", "activity", "presence", "end", "resume", "replay", "stale", "unsequenced"} {
					actions[kind] = func(property *rapid.T) { machine.report(property, kind) }
				}
				property.Repeat(actions)
			})
		})
	}
}

func (machine *registryStateMachine) open(property *rapid.T) {
	property.Helper()
	if machine.backend == "journal" {
		journal := NewJournal(machine.path, fixtureRules{})
		journal.setNowForTest(func() time.Time { return machine.clock })
		machine.store = journal
		return
	}
	memory, err := OpenMemoryStoreWithOptions(machine.path, fixtureRules{}, MemoryStoreOptions{TombstoneTTL: machine.ttl})
	if err != nil {
		property.Fatal(err)
	}
	memory.setNowForTest(func() time.Time { return machine.clock })
	machine.memory = memory
	machine.store = memory
}

func (machine *registryStateMachine) command(property *rapid.T, index int, kind string) registryModelCommand {
	id := machine.names[index]
	previous, exists := machine.sessions[id]
	process := previous.Value.Process
	if !exists || kind == "resume" || (kind == "start" && previous.Value.Presence == PresenceGone) {
		machine.generations[id]++
		process = ProcessIdentity{PID: 100 + index, StartIdentity: id + ":" + strconv.Itoa(machine.generations[id]), Executable: "/agent", CWD: rapid.SampledFrom([]string{"/work/alpha", "/work/beta"}).Draw(property, "process cwd")}
	}
	machine.clock = machine.clock.Add(time.Millisecond)
	activity := rapid.SampledFrom([]Activity{ActivityIdle, ActivityRunning, ActivityWaiting, ActivityFailed, ActivityInterrupted, ActivityUnknown}).Draw(property, "activity")
	if kind == "presence" || kind == "end" {
		activity = ""
	}
	return registryModelCommand{ID: id, Kind: kind, Activity: activity, Process: process, Sequence: previous.Value.Sequence + rapid.Uint64Range(2, 10).Draw(property, "sequence increase"), At: machine.clock}
}

func registryModelObservation(command registryModelCommand) Observation {
	report := &Report{Reporter: Reporter{Integration: "model-extension", Sequence: new(command.Sequence)}, Event: command.Kind, Claim: new(PresenceLive), Process: new(command.Process)}
	if command.Activity != "" {
		report.Activity = new(command.Activity)
	}
	switch command.Kind {
	case "start", "presence":
		report.Lifecycle = new(NativeLifecycleStart)
	case "resume":
		report.Lifecycle = new(NativeLifecycleResume)
	case "end":
		report.Lifecycle = new(NativeLifecycleEnd)
		report.Claim = new(PresenceGone)
	}
	return Observation{Harness: "model", At: command.At, Subject: ObservationIdentity{SessionID: command.ID, CWD: command.Process.CWD}, Evidence: report}
}

func (machine *registryStateMachine) accept(command registryModelCommand) {
	previous, exists := machine.sessions[command.ID]
	revives := command.Kind == "start" || command.Kind == "resume" || command.Kind == "presence"
	if exists && previous.Value.Presence == PresenceGone && !revives {
		return
	}
	current := previous
	if !exists || previous.Value.Presence == PresenceGone || previous.Value.Process != command.Process {
		current.Value.Activity = ActivityUnknown
		current.Value.NativeActivity = ""
	}
	current.Value.Presence = PresenceLive
	current.Value.Process = command.Process
	current.Value.CWD = command.Process.CWD
	current.Value.Sequence = command.Sequence
	current.Value.Event = command.Kind
	current.Value.NativeEnded = command.Kind == "end"
	current.NativeAt = command.At
	if command.Activity != "" {
		current.Value.Activity = command.Activity
		current.Value.NativeActivity = command.Activity
	} else if current.Value.NativeActivity != "" {
		current.Value.Activity = current.Value.NativeActivity
	}
	if command.Kind == "end" {
		current.Value.Presence = PresenceGone
		current.Value.Activity = ""
		current.Value.NativeActivity = ""
		current.GoneAt = command.At
	}
	machine.sessions[command.ID] = current
}

func (machine *registryStateMachine) report(property *rapid.T, kind string) {
	index := rapid.IntRange(0, len(machine.names)-1).Draw(property, "session")
	command := machine.command(property, index, kind)
	if kind == "replay" || kind == "stale" || kind == "unsequenced" {
		machine.rejectSequence(property, command)
		return
	}
	if _, err := machine.store.Observe(property.Context(), registryModelObservation(command)); err != nil {
		property.Fatalf("%s report: %v", kind, err)
	}
	machine.accept(command)
	machine.expire()
}

func (machine *registryStateMachine) rejectSequence(property *rapid.T, command registryModelCommand) {
	previous, exists := machine.sessions[command.ID]
	if !exists || previous.Value.Sequence == 0 {
		command.Kind = "start"
		if _, err := machine.store.Observe(property.Context(), registryModelObservation(command)); err != nil {
			property.Fatal(err)
		}
		machine.accept(command)
		machine.expire()
		return
	}
	observation := registryModelObservation(command)
	switch command.Kind {
	case "replay":
		observation.Report().Reporter.Sequence = new(previous.Value.Sequence)
	case "stale":
		observation.Report().Reporter.Sequence = new(previous.Value.Sequence - 1)
	case "unsequenced":
		observation.Report().Reporter.Sequence = nil
	}
	if _, err := machine.store.Observe(property.Context(), observation); !errors.Is(err, ErrObservationConflict) {
		property.Fatalf("%s sequence error = %v, want conflict", command.Kind, err)
	}
}

func (machine *registryStateMachine) screen(property *rapid.T) {
	index := rapid.IntRange(0, len(machine.names)-1).Draw(property, "session")
	id := machine.names[index]
	previous, exists := machine.sessions[id]
	if !exists {
		command := machine.command(property, index, "start")
		if _, err := machine.store.Observe(property.Context(), registryModelObservation(command)); err != nil {
			property.Fatal(err)
		}
		machine.accept(command)
		previous = machine.sessions[id]
	}
	machine.clock = machine.clock.Add(time.Millisecond)
	activity := rapid.SampledFrom([]Activity{ActivityIdle, ActivityRunning, ActivityWaiting, ActivityFailed, ActivityInterrupted, ActivityUnknown}).Draw(property, "screen activity")
	observation := Observation{Harness: "model", At: machine.clock, Subject: ObservationIdentity{SessionID: id}, Evidence: &Reading{Activity: activity, Authority: AuthorityScreen, Reason: "manifest_rule", Process: previous.Value.Process}}
	if _, err := machine.store.Observe(property.Context(), observation); err != nil {
		property.Fatal(err)
	}
	if previous.Value.Presence != PresenceGone && (previous.Value.NativeActivity == "" || previous.Value.NativeActivity == ActivityUnknown || machine.clock.Sub(previous.NativeAt) > IntegrationActivityLease) {
		previous.Value.Activity = activity
		machine.sessions[id] = previous
	}
	machine.expire()
}

func (machine *registryStateMachine) processGone(property *rapid.T) {
	index := rapid.IntRange(0, len(machine.names)-1).Draw(property, "session")
	id := machine.names[index]
	if _, exists := machine.sessions[id]; !exists {
		command := machine.command(property, index, "start")
		if _, err := machine.store.Observe(property.Context(), registryModelObservation(command)); err != nil {
			property.Fatal(err)
		}
		machine.accept(command)
	}
	previous := machine.sessions[id]
	machine.clock = machine.clock.Add(time.Millisecond)
	observation := Observation{Harness: "model", At: machine.clock, Subject: ObservationIdentity{SessionID: id}, Evidence: &Sighting{Process: previous.Value.Process, Present: false}}
	if _, err := machine.store.Observe(property.Context(), observation); err != nil {
		property.Fatal(err)
	}
	if previous.Value.Presence != PresenceGone {
		previous.GoneAt = machine.clock
	}
	previous.Value.Presence = PresenceGone
	previous.Value.Activity = ""
	machine.sessions[id] = previous
	machine.expire()
}

func (machine *registryStateMachine) invalidBatch(property *rapid.T) {
	index := rapid.IntRange(0, len(machine.names)-1).Draw(property, "session")
	command := machine.command(property, index, "resume")
	invalid := registryModelObservation(command)
	invalid.Harness = "unknown"
	batch := []Observation{registryModelObservation(command), invalid}
	if _, err := machine.store.ObserveBatch(property.Context(), batch); !errors.Is(err, ErrInvalidObservation) {
		property.Fatalf("invalid batch error = %v, want invalid observation", err)
	}
}

func (machine *registryStateMachine) batch(property *rapid.T) {
	first := rapid.IntRange(0, len(machine.names)-1).Draw(property, "first session")
	second := (first + rapid.IntRange(1, len(machine.names)-1).Draw(property, "second session offset")) % len(machine.names)
	commands := []registryModelCommand{machine.command(property, first, "resume"), machine.command(property, second, "activity")}
	observations := []Observation{registryModelObservation(commands[0]), registryModelObservation(commands[1])}
	if _, err := machine.store.ObserveBatch(property.Context(), observations); err != nil {
		property.Fatal(err)
	}
	for _, command := range commands {
		machine.accept(command)
	}
	machine.expire()
}

func (machine *registryStateMachine) gc(property *rapid.T) {
	ages := []time.Duration{0, time.Duration(rapid.IntRange(0, 12).Draw(property, "GC age minutes")) * time.Minute}
	for _, id := range machine.names {
		session, exists := machine.sessions[id]
		if !exists || session.Value.Presence != PresenceGone {
			continue
		}
		elapsed := machine.clock.Sub(session.GoneAt)
		ages = append(ages, elapsed, elapsed+time.Nanosecond)
		if elapsed > 0 {
			ages = append(ages, elapsed-time.Nanosecond)
		}
	}
	age := rapid.SampledFrom(ages).Draw(property, "GC age")
	deleted := 0
	for id, session := range machine.sessions {
		if session.Value.Presence == PresenceGone && machine.clock.Sub(session.GoneAt) >= age {
			delete(machine.sessions, id)
			delete(machine.handles, id)
			deleted++
		}
	}
	result, err := machine.store.GC(property.Context(), age)
	if err != nil {
		property.Fatal(err)
	}
	if result.Deleted != deleted || result.Remaining != len(machine.sessions) {
		property.Fatalf("GC = %#v, want deleted=%d remaining=%d", result, deleted, len(machine.sessions))
	}
	machine.expire()
}

func (machine *registryStateMachine) advance(property *rapid.T) {
	machine.clock = machine.clock.Add(time.Duration(rapid.IntRange(0, 12).Draw(property, "elapsed minutes")) * time.Minute)
	if _, err := machine.store.ObserveBatch(property.Context(), nil); err != nil {
		property.Fatal(err)
	}
	machine.expire()
}

func (machine *registryStateMachine) expire() {
	if machine.backend != "memory" {
		return
	}
	for id, session := range machine.sessions {
		if session.Value.Presence == PresenceGone && machine.clock.Sub(session.GoneAt) >= machine.ttl {
			delete(machine.sessions, id)
			delete(machine.handles, id)
		}
	}
}

func (machine *registryStateMachine) reset(property *rapid.T) {
	result, err := machine.store.Reset(property.Context())
	if err != nil {
		property.Fatal(err)
	}
	if result.Cleared != len(machine.sessions) || result.Remaining != 0 {
		property.Fatalf("reset = %#v, want cleared=%d remaining=0", result, len(machine.sessions))
	}
	clear(machine.sessions)
	clear(machine.handles)
}

func (machine *registryStateMachine) reload(property *rapid.T) {
	if machine.memory != nil {
		if err := machine.memory.Close(); err != nil {
			property.Fatal(err)
		}
	}
	machine.open(property)
}

func (machine *registryStateMachine) check(property *rapid.T) {
	property.Helper()
	sessions, err := machine.store.List(property.Context(), Filter{})
	if err != nil {
		property.Fatal(err)
	}
	got := map[string]registryModelValue{}
	for _, session := range sessions {
		if _, duplicate := got[session.SessionID]; duplicate {
			property.Fatalf("duplicate native identity %q", session.SessionID)
		}
		if session.Harness != "model" || session.IdentityState != IdentityIdentified || session.Process == nil {
			property.Fatalf("native identity lost: %#v", session)
		}
		if handle, exists := machine.handles[session.SessionID]; exists && handle != session.ID {
			property.Fatalf("durable identity changed from %q to %q", handle, session.ID)
		}
		machine.handles[session.SessionID] = session.ID
		got[session.SessionID] = registryModelProjection(session)
		machine.checkGet(property, session)
	}
	want := map[string]registryModelValue{}
	for id, session := range machine.sessions {
		want[id] = session.Value
	}
	if diff := cmp.Diff(want, got); diff != "" {
		property.Fatalf("%s disagrees with independent model (-model +store):\n%s", machine.backend, diff)
	}
	machine.checkFilter(property)
}

func (machine *registryStateMachine) checkGet(property *rapid.T, listed Session) {
	property.Helper()
	want, exists := machine.sessions[listed.SessionID]
	if !exists || listed.ID == "" {
		property.Fatalf("unexpected identity %#v", listed)
	}
	got, err := machine.store.Get(property.Context(), listed.ID)
	if err != nil {
		property.Fatal(err)
	}
	if got.SessionID != listed.SessionID || got.Process == nil {
		property.Fatalf("Get lost native identity: %#v", got)
	}
	if diff := cmp.Diff(want.Value, registryModelProjection(got)); diff != "" {
		property.Fatalf("Get disagrees with independent model (-model +store):\n%s", diff)
	}
}

func (machine *registryStateMachine) checkFilter(property *rapid.T) {
	filter := Filter{
		Presence: rapid.SampledFrom([]Presence{"", PresenceLive, PresenceGone, PresenceUnknown}).Draw(property, "presence filter"),
		Activity: rapid.SampledFrom([]Activity{"", ActivityIdle, ActivityRunning, ActivityWaiting, ActivityFailed, ActivityInterrupted, ActivityUnknown}).Draw(property, "activity filter"),
	}
	filtered, err := machine.store.List(property.Context(), filter)
	if err != nil {
		property.Fatal(err)
	}
	want := map[string]registryModelValue{}
	for id, session := range machine.sessions {
		if (filter.Presence == "" || filter.Presence == session.Value.Presence) && (filter.Activity == "" || filter.Activity == session.Value.Activity) {
			want[id] = session.Value
		}
	}
	got := map[string]registryModelValue{}
	for _, session := range filtered {
		if session.Process == nil {
			property.Fatalf("filtered session lost process: %#v", session)
		}
		got[session.SessionID] = registryModelProjection(session)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		property.Fatalf("filtered result disagrees with independent model (-model +store):\n%s", diff)
	}
}

func registryModelProjection(session Session) registryModelValue {
	value := registryModelValue{Presence: session.Presence(), CWD: session.CWD, Process: *session.Process, NativeEnded: session.Incarnation.NativeEnded}
	if activity := session.Activity(); activity != nil {
		value.Activity = *activity
	}
	if native := session.Observations.Native; native != nil {
		value.Event = native.Event
		if native.Reporter.Sequence != nil {
			value.Sequence = *native.Reporter.Sequence
		}
		if native.Activity != nil {
			value.NativeActivity = *native.Activity
		}
	}
	return value
}

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
