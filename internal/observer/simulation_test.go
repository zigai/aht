package observer

import (
	"context"
	"errors"
	"fmt"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/v2/internal/agentstate"
	harness "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/internal/registrycheck"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

type simulationEvent = string

var (
	errSimulationProperty = errors.New("simulation property violation")
	errSimulationProcess  = errors.New("simulated process enumeration failure")
	errSimulationPane     = errors.New("simulated pane enumeration failure")
	errSimulationCatalog  = errors.New("simulated catalog enumeration failure")
)

const (
	simStart      simulationEvent = "start"
	simExit       simulationEvent = "exit"
	simPaneClose  simulationEvent = "pane-close"
	simPaneReuse  simulationEvent = "pane-reuse"
	simPIDReuse   simulationEvent = "pid-reuse"
	simHookStart  simulationEvent = "hook-start"
	simHookRun    simulationEvent = "hook-run"
	simHookWait   simulationEvent = "hook-wait"
	simHookEnd    simulationEvent = "hook-end"
	simHookReplay simulationEvent = "hook-replay"
	simHookStale  simulationEvent = "hook-stale"
	simScreen     simulationEvent = "screen"
	simCatalog    simulationEvent = "catalog"
	simProcessErr simulationEvent = "process-error"
	simPaneErr    simulationEvent = "pane-error"
	simCatalogErr simulationEvent = "catalog-error"
	simTick       simulationEvent = "tick"
)

type simulationProcess struct {
	process   processinfo.Process
	sessionID string
	sequence  uint64
	last      *registry.Report
	missing   time.Time
}

type simulationWorld struct {
	store       *registry.MemoryStore
	observer    *Observer
	harness     registry.Harness
	command     string
	reporter    string
	processes   [3]*simulationProcess
	last        [3]*simulationProcess
	panes       [3]bool
	catalog     [3]bool
	screens     [3]registry.Activity
	processErr  bool
	paneErr     bool
	catalogErr  bool
	incarnation int
	coverage    *registrycheck.Coverage
	absentSince map[string]time.Time
	recovering  *simulationProcess
}

func TestObserverSeededWorld(t *testing.T) {
	seeds, steps := uint64(4), 36
	if testing.Short() {
		seeds, steps = 2, 22
	} else if os.Getenv("AHT_EXTENDED_PARITY") != "" {
		seeds, steps = 16, 160
	}
	coverage := registrycheck.NewCoverage()
	allPassed := true
	for seed := range seeds {
		passed := t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { runSimulationSeed(t, seed, steps, coverage) })
		})
		allPassed = allPassed && passed
	}
	if !allPassed {
		return
	}
	if missing := coverage.Missing(simStart, simExit, simPaneClose, simPaneReuse, simPIDReuse, simHookStart, simHookRun, simHookWait, simHookEnd, simHookReplay, simHookStale, simScreen, simCatalog, simProcessErr, simPaneErr, simCatalogErr, "gone", "recovered", "hook-authority", "screen-reading", "stale-rejected", "replay-handled"); len(missing) > 0 {
		t.Errorf("events or outcomes not covered: %v", missing)
	}
}

func runSimulationSeed(t *testing.T, seed uint64, steps int, coverage *registrycheck.Coverage) {
	t.Helper()
	world := newSimulationWorld(t, coverage)
	rng := rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)
	trace := make([]string, 0, steps)
	for step := range steps {
		event, slot := simulationChoice(rng, step)
		trace = append(trace, fmt.Sprintf("%d:%s/%d", step, event, slot))
		world.step(t, seed, trace, event, slot)
		world.coverage.Hit(event)
		time.Sleep(time.Second)
	}
	if err := world.store.Close(); err != nil {
		t.Fatalf("seed=%d: close store: %v", seed, err)
	}
}

func (w *simulationWorld) step(t *testing.T, seed uint64, trace []string, event simulationEvent, slot int) {
	t.Helper()
	if err := w.apply(t.Context(), event, slot); err != nil {
		t.Fatalf("seed=%d trace=%v: %v", seed, trace, err)
	}
	before, err := w.store.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatalf("seed=%d trace=%v: list before: %v", seed, trace, err)
	}
	result, runErr := w.observer.RunOnce(t.Context())
	if event == simProcessErr && (runErr == nil || !result.Degraded) {
		t.Fatalf("seed=%d trace=%v: process error result=%+v err=%v", seed, trace, result, runErr)
	}
	if event != simProcessErr && runErr != nil {
		t.Fatalf("seed=%d trace=%v: cycle: %v", seed, trace, runErr)
	}
	sessions, err := w.store.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatalf("seed=%d trace=%v: list after: %v", seed, trace, err)
	}
	if err := registrycheck.Sessions(sessions); err != nil {
		t.Fatalf("seed=%d trace=%v: shared session invariants: %v", seed, trace, err)
	}
	if err := w.check(before, sessions, event, result, time.Now()); err != nil {
		t.Fatalf("seed=%d trace=%v: %v", seed, trace, err)
	}
}

func simulationChoice(rng *rand.PCG, step int) (simulationEvent, int) {
	script := []simulationEvent{simStart, simScreen, simHookStart, simScreen, simHookWait, simHookReplay, simHookStale, simExit, simProcessErr, simStart, simHookRun, simExit, simHookRun, simTick, simTick, simPaneClose, simPaneReuse, simPIDReuse, simCatalog, simPaneErr, simCatalogErr, simHookEnd}
	if step < len(script) {
		return script[step], 0
	}
	events := []simulationEvent{simStart, simExit, simPaneClose, simPaneReuse, simPIDReuse, simHookStart, simHookRun, simHookWait, simHookEnd, simHookReplay, simHookStale, simScreen, simCatalog, simProcessErr, simPaneErr, simCatalogErr, simTick}
	position := rng.Uint64() % uint64(len(events))
	chosen := events[0]
	for _, event := range events {
		if position == 0 {
			chosen = event
			break
		}
		position--
	}
	slot := 0
	switch rng.Uint64() % 3 {
	case 1:
		slot = 1
	case 2:
		slot = 2
	}
	return chosen, slot
}

func newSimulationWorld(t *testing.T, coverage *registrycheck.Coverage) *simulationWorld {
	t.Helper()
	id, command, reporter := simulationHarness(t)
	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), harness.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	world := &simulationWorld{store: store, harness: id, command: command, reporter: reporter, coverage: coverage, absentSince: make(map[string]time.Time)}
	world.observer = New(Options{Store: store, Now: time.Now, Interval: time.Second, GracePeriod: 2 * time.Second, ProcessList: world.listProcesses, PaneList: world.listPanes, CatalogList: world.listCatalog, ScreenCapture: world.captureScreen, DetectionConfigDir: t.TempDir()})
	return world
}

func simulationHarness(t *testing.T) (registry.Harness, string, string) {
	t.Helper()
	var id registry.Harness
	var command, reporter string
	for _, adapter := range harness.All() {
		definition := adapter.Definition()
		policy := (harness.Rules{}).Policy(definition.ID)
		if !policy.ExclusiveProcess || policy.Authority != registry.AuthorityHook || !policy.ScreenFallback || !harness.SupportsScreen(definition.ID) || len(definition.ProcessNames) == 0 {
			continue
		}
		if !simulationScreenFrames(t, definition.ID) {
			continue
		}
		candidate := definition.ProcessNames[0]
		if resolved, ok := resolveHarness(processinfo.Process{PID: 901, StartIdentity: "boot:1", Executable: candidate, Args: []string{candidate}}); ok && resolved == definition.ID {
			id, command, reporter = definition.ID, candidate, policy.Reporter
			break
		}
	}
	if id == "" {
		t.Fatal("no observable exclusive hook harness with screen fallback")
	}
	return id, command, reporter
}

func simulationScreenFrames(t *testing.T, id registry.Harness) bool {
	t.Helper()
	manifest, err := (agentstate.Loader{ConfigDir: t.TempDir()}).Load(id)
	if err != nil {
		return false
	}
	return manifest.Evaluate(agentstate.NormalizeSnapshot("Type a message", "")).Activity == registry.ActivityIdle && manifest.Evaluate(agentstate.NormalizeSnapshot("Waiting for input", "")).Activity == registry.ActivityWaiting
}

func (w *simulationWorld) spawn(slot int, reusePID bool) {
	if current := w.processes[slot]; current != nil {
		w.absentSince[simulationIdentity(current.process)] = time.Now()
	}
	w.incarnation++
	pid := 900 + slot
	if !reusePID {
		pid = 1000 + w.incarnation
	}
	command := w.command
	process := processinfo.Process{PID: pid, PPID: 1, ProcessGroupID: pid, Foreground: true, StartIdentity: fmt.Sprintf("boot:%d", w.incarnation), Executable: "/usr/bin/" + command, Args: []string{command}, CWD: fmt.Sprintf("/work/%d", slot), TTY: fmt.Sprintf("/dev/pts/%d", slot+1)}
	w.processes[slot] = &simulationProcess{process: process, sessionID: fmt.Sprintf("session-%d", w.incarnation)}
	w.last[slot] = w.processes[slot]
	w.panes[slot] = true
	w.catalog[slot] = true
	w.screens[slot] = registry.ActivityIdle
}

func (w *simulationWorld) apply(ctx context.Context, event simulationEvent, slot int) error {
	w.processErr, w.paneErr, w.catalogErr = false, false, false
	w.recovering = nil
	switch event {
	case simStart:
		w.start(slot)
	case simExit:
		w.exit(slot)
	case simPaneClose:
		w.panes[slot] = false
	case simPaneReuse:
		w.spawn(slot, false)
	case simPIDReuse:
		w.spawn(slot, true)
	case simScreen:
		if w.screens[slot] == registry.ActivityIdle {
			w.screens[slot] = registry.ActivityWaiting
		} else {
			w.screens[slot] = registry.ActivityIdle
		}
	case simCatalog:
		w.catalog[slot] = !w.catalog[slot]
	default:
		return w.applySecondary(ctx, event, slot)
	}
	return nil
}

func (w *simulationWorld) start(slot int) {
	if w.processes[slot] != nil {
		return
	}
	previous := w.last[slot]
	if previous == nil || previous.missing.IsZero() || time.Since(previous.missing) > 2*time.Second {
		w.spawn(slot, false)
		return
	}
	w.processes[slot] = previous
	w.panes[slot] = true
	w.recovering = previous
	delete(w.absentSince, simulationIdentity(previous.process))
}

func (w *simulationWorld) exit(slot int) {
	current := w.processes[slot]
	if current == nil {
		return
	}
	current.missing = time.Now()
	w.absentSince[simulationIdentity(current.process)] = current.missing
	w.last[slot] = current
	w.processes[slot] = nil
}

func (w *simulationWorld) applySecondary(ctx context.Context, event simulationEvent, slot int) error {
	switch event {
	case simHookStart, simHookRun, simHookWait, simHookEnd, simHookReplay, simHookStale:
		return w.report(ctx, event, slot)
	case simProcessErr:
		w.processErr = true
	case simPaneErr:
		w.paneErr = true
	case simCatalogErr:
		w.catalogErr = true
	case simTick:
	default:
		return fmt.Errorf("%w: unknown event %s", errSimulationProperty, event)
	}
	return nil
}

func (w *simulationWorld) report(ctx context.Context, event simulationEvent, slot int) error {
	actor := w.processes[slot]
	if actor == nil {
		actor = w.last[slot]
	}
	if actor == nil {
		return nil
	}
	previousSequence, err := w.previousSequence(ctx, actor.sessionID)
	if err != nil {
		return err
	}
	report := w.makeReport(actor, event)
	if report == nil {
		return nil
	}
	_, err = w.store.Observe(ctx, registry.Observation{Harness: w.harness, At: time.Now(), Subject: registry.ObservationIdentity{SessionID: actor.sessionID}, Evidence: report})
	if event == simHookReplay || event == simHookStale {
		return w.checkReplayedReport(event, err, previousSequence, report)
	}
	if err != nil {
		return fmt.Errorf("recording simulated report: %w", err)
	}
	return nil
}

func (w *simulationWorld) checkReplayedReport(event simulationEvent, err error, previousSequence uint64, report *registry.Report) error {
	if errors.Is(err, registry.ErrObservationConflict) {
		if event == simHookStale {
			w.coverage.Hit("stale-rejected")
		} else {
			w.coverage.Hit("replay-handled")
		}
		return nil
	}
	if err == nil && report.Reporter.Sequence != nil && *report.Reporter.Sequence < previousSequence {
		return simulationError("stale sequence %d accepted after %d", *report.Reporter.Sequence, previousSequence)
	}
	if err != nil {
		return fmt.Errorf("recording replayed report: %w", err)
	}
	if event == simHookReplay {
		w.coverage.Hit("replay-handled")
	}
	return nil
}

func (w *simulationWorld) previousSequence(ctx context.Context, sessionID string) (uint64, error) {
	sessions, err := w.store.List(ctx, registry.Filter{})
	if err != nil {
		return 0, fmt.Errorf("listing report sequence: %w", err)
	}
	for _, session := range sessions {
		if session.SessionID == sessionID && session.Observations.Native != nil && session.Observations.Native.Reporter.Sequence != nil {
			return *session.Observations.Native.Reporter.Sequence, nil
		}
	}
	return 0, nil
}

func (w *simulationWorld) makeReport(actor *simulationProcess, event simulationEvent) *registry.Report {
	if event == simHookReplay || event == simHookStale {
		if actor.last == nil {
			return nil
		}
		report := *actor.last
		if event == simHookStale && actor.sequence > 1 {
			sequence := actor.sequence - 1
			report.Reporter.Sequence = &sequence
		}
		return &report
	}
	actor.sequence++
	sequence := actor.sequence
	report := &registry.Report{Reporter: registry.Reporter{Integration: w.reporter, Version: harness.IntegrationVersionFor(w.harness), Sequence: &sequence}, Event: event, Process: processIdentity(actor.process)}
	switch event {
	case simHookStart:
		lifecycle, presence, activity := registry.NativeLifecycleStart, registry.PresenceLive, registry.ActivityRunning
		report.Lifecycle, report.Claim, report.Activity = &lifecycle, &presence, &activity
	case simHookRun:
		activity := registry.ActivityRunning
		report.Activity = &activity
	case simHookWait:
		activity := registry.ActivityWaiting
		report.Activity = &activity
	case simHookEnd:
		lifecycle, presence := registry.NativeLifecycleEnd, registry.PresenceGone
		report.Lifecycle, report.Claim = &lifecycle, &presence
	}
	actor.last = report
	return report
}

func (w *simulationWorld) listProcesses(context.Context) ([]processinfo.Process, error) {
	if w.processErr {
		return nil, errSimulationProcess
	}
	var result []processinfo.Process
	for _, actor := range w.processes {
		if actor != nil {
			result = append(result, actor.process)
		}
	}
	return result, nil
}

func (w *simulationWorld) listPanes(context.Context) ([]mux.Pane, error) {
	if w.paneErr {
		return nil, errSimulationPane
	}
	var result []mux.Pane
	for slot, actor := range w.processes {
		if actor == nil || !w.panes[slot] {
			continue
		}
		process := actor.process
		result = append(result, mux.Pane{Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "sim", SessionID: "$sim", PaneID: fmt.Sprintf("%%%d", slot), PanePID: process.PID, PaneCurrentPath: process.CWD, PaneTTY: process.TTY}, Processes: []mux.ProcessRef{{PID: process.PID, Command: w.command, CWD: process.CWD}}, ProcessTTY: process.TTY, Command: w.command, CWD: process.CWD})
	}
	return result, nil
}

func (w *simulationWorld) listCatalog(context.Context) ([]CatalogEntry, error) {
	if w.catalogErr {
		return nil, errSimulationCatalog
	}
	var result []CatalogEntry
	for slot, actor := range w.processes {
		if actor != nil && w.catalog[slot] {
			result = append(result, CatalogEntry{Harness: w.harness, SessionID: actor.sessionID, CWD: actor.process.CWD, ProcessPID: actor.process.PID, Current: true})
		}
	}
	return result, nil
}

func (w *simulationWorld) captureScreen(_ context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	for slot := range w.screens {
		if pane.Location.PaneID == fmt.Sprintf("%%%d", slot) {
			if w.screens[slot] == registry.ActivityWaiting {
				return mux.ScreenSnapshot{Text: "Waiting for input"}, nil
			}
			return mux.ScreenSnapshot{Text: "Type a message"}, nil
		}
	}
	return mux.ScreenSnapshot{}, simulationError("unknown pane %s", pane.Location.PaneID)
}

func (w *simulationWorld) check(before, after []registry.Session, event simulationEvent, result Result, now time.Time) error {
	if err := w.checkSessions(after, now); err != nil {
		return err
	}
	if event == simProcessErr {
		if err := checkDegradedPresence(before, after); err != nil {
			return err
		}
	}
	if event == simPaneErr || event == simCatalogErr {
		if err := w.checkLiveProcessesPreserved(before, after); err != nil {
			return err
		}
	}
	if result.Gone > 0 {
		w.coverage.Hit("gone")
	}
	if err := w.checkRecovery(after); err != nil {
		return err
	}
	for _, session := range after {
		if session.Observations.Screen != nil {
			w.coverage.Hit("screen-reading")
			break
		}
	}
	return nil
}

func (w *simulationWorld) checkLiveProcessesPreserved(before, after []registry.Session) error {
	states := make(map[string]registry.Presence, len(after))
	for _, session := range after {
		states[session.ID] = session.Presence()
	}
	for _, session := range before {
		if session.Presence() == registry.PresenceLive && session.Process != nil && w.hasProcess(*session.Process) && states[session.ID] == registry.PresenceGone {
			return simulationError("degraded auxiliary enumeration retired running process %s", session.ID)
		}
	}
	return nil
}

func (w *simulationWorld) checkRecovery(sessions []registry.Session) error {
	if w.recovering == nil {
		return nil
	}
	for _, session := range sessions {
		if session.Process != nil && session.Process.Equal(*processIdentity(w.recovering.process)) && session.Presence() == registry.PresenceLive {
			w.coverage.Hit("recovered")
			return nil
		}
	}
	return simulationError("returning process %s did not recover to live", simulationIdentity(w.recovering.process))
}

func (w *simulationWorld) checkSessions(sessions []registry.Session, now time.Time) error {
	if err := simulationSessions(sessions); err != nil {
		return err
	}
	paneOwners := make(map[string]string)
	for _, session := range sessions {
		if session.Presence() != registry.PresenceLive {
			continue
		}
		if session.Process == nil || !session.Process.Complete() {
			continue
		}
		if err := w.checkSession(session, now, paneOwners); err != nil {
			return err
		}
	}
	return nil
}

func simulationSessions(sessions []registry.Session) error {
	owners := make(map[string]string)
	for _, session := range sessions {
		if session.Presence() != registry.PresenceLive || session.Process == nil || !session.Process.Complete() || !(harness.Rules{}).Policy(session.Harness).ExclusiveProcess {
			continue
		}
		identity := simulationIdentity(processinfo.Process{PID: session.Process.PID, StartIdentity: session.Process.StartIdentity})
		if owner := owners[identity]; owner != "" && owner != session.ID {
			return simulationError("two live sessions own process %s: %s, %s", identity, owner, session.ID)
		}
		owners[identity] = session.ID
	}
	return nil
}

func (w *simulationWorld) checkSession(session registry.Session, now time.Time, paneOwners map[string]string) error {
	identity := string(session.Harness) + "/" + simulationIdentity(processinfo.Process{PID: session.Process.PID, StartIdentity: session.Process.StartIdentity})
	if session.Location.PaneID != "" && w.hasProcess(*session.Process) {
		pane := fmt.Sprintf("%s/%s/%s", session.Location.Kind, session.Location.ServerID, session.Location.PaneID)
		if owner := paneOwners[pane]; owner != "" && owner != session.ID {
			return simulationError("two live sessions with running processes own pane %s: %s, %s", pane, owner, session.ID)
		}
		paneOwners[pane] = session.ID
	}
	if missingAt, absent := w.absentSince[simulationIdentity(processinfo.Process{PID: session.Process.PID, StartIdentity: session.Process.StartIdentity})]; !w.processErr && absent && !w.hasProcess(*session.Process) && now.Sub(missingAt) >= 3*time.Second {
		return simulationError("ghost live session %s for process %s", session.ID, identity)
	}
	return w.checkHookAuthority(session, now)
}

func (w *simulationWorld) checkHookAuthority(session registry.Session, now time.Time) error {
	policy := (harness.Rules{}).Policy(session.Harness)
	authority, _ := registry.ActivityAuthority(session, policy, now)
	if authority != registry.AuthorityHook || session.Observations.Native == nil || session.Observations.Native.Activity == nil || !registry.EvaluateHook(session, policy, now).Active {
		return nil
	}
	if session.Observations.Screen != nil && *session.Observations.Native.Activity != session.Observations.Screen.Activity {
		w.coverage.Hit("hook-authority")
	}
	if activity := session.Activity(); activity == nil || *activity != *session.Observations.Native.Activity {
		return simulationError("fresh hook activity lost for %s: got %v, report %v", session.ID, activity, session.Observations.Native.Activity)
	}
	if decision := session.Decision(); decision == nil || decision.Authority != registry.AuthorityHook {
		return simulationError("fresh hook decision lost for %s: %v", session.ID, decision)
	}
	return nil
}

func simulationIdentity(process processinfo.Process) string {
	return fmt.Sprintf("%d/%s", process.PID, process.StartIdentity)
}

func (w *simulationWorld) hasProcess(identity registry.ProcessIdentity) bool {
	for _, actor := range w.processes {
		if actor != nil && actor.process.PID == identity.PID && actor.process.StartIdentity == identity.StartIdentity {
			return true
		}
	}
	return false
}

func checkDegradedPresence(before, after []registry.Session) error {
	states := make(map[string]registry.Presence, len(after))
	for _, session := range after {
		states[session.ID] = session.Presence()
	}
	for _, session := range before {
		if session.Presence() == registry.PresenceLive && states[session.ID] == registry.PresenceGone {
			return simulationError("process enumeration error retired live session %s", session.ID)
		}
	}
	return nil
}

func simulationError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errSimulationProperty, fmt.Sprintf(format, args...))
}
