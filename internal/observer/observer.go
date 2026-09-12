package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zigai/aht/internal/agentstate"
	harness "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

const (
	defaultObserverInterval    = time.Second
	defaultMissingSnapshots    = 2
	commandArgumentPrefixCount = 2
	initialScreenConfirmations = 2
)

var (
	errObserverContextNil       = errors.New("observer context is nil")
	errDetectionOverrideInvalid = errors.New("agent detection override is invalid")
	errObserverCycleDegraded    = errors.New("observer cycle degraded")
	ErrAlreadyRunning           = errors.New("observer is already running")
)

type ProcessLister func(context.Context) ([]processinfo.Process, error)

type CatalogEntry struct {
	Harness       registry.Harness `json:"harness"`
	SessionID     string           `json:"session_id,omitempty"`
	SessionPath   string           `json:"session_path,omitempty"`
	ResumeCommand []string         `json:"resume_command,omitempty"`
	CWD           string           `json:"cwd,omitempty"`
	ProjectRoot   string           `json:"project_root,omitempty"`
	ProcessPID    int              `json:"process_pid,omitempty"`
	Current       bool             `json:"current"`
}
type CatalogLister func(context.Context) ([]CatalogEntry, error)

// Store is the observation and inventory capability required by an Observer.
type Store interface {
	Observe(ctx context.Context, observation registry.Observation) (registry.Session, error)
	ObserveBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error)
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
}

type Options struct {
	Store         Store
	StorePath     string
	Interval      time.Duration
	GracePeriod   time.Duration
	HealthPath    string
	ProcessList   ProcessLister
	PaneList      mux.PaneLister
	CatalogList   CatalogLister
	ScreenCapture mux.ScreenCapturer
	// DisableScreenInspection disables terminal capture and screen-derived state.
	// Native multiplexer activity remains available without reading screen text.
	DisableScreenInspection bool
	DetectionConfigDir      string
	Now                     func() time.Time
	ErrorWriter             io.Writer
	Quiet                   bool
}

type Result struct {
	ObservedAt   time.Time `json:"observed_at"`
	Observations int       `json:"observations"`
	Sessions     int       `json:"sessions"`
	Processes    int       `json:"processes"`
	Panes        int       `json:"panes"`
	Catalog      int       `json:"catalog"`
	Present      int       `json:"present"`
	Gone         int       `json:"gone"`
	Changed      int       `json:"changed"`
	Degraded     bool      `json:"degraded"`
	Error        string    `json:"error,omitempty"`
}

type Health struct {
	PID                          int           `json:"pid"`
	StartIdentity                string        `json:"start_identity,omitempty"`
	Interval                     time.Duration `json:"interval"`
	GracePeriod                  time.Duration `json:"grace_period"`
	StartedAt                    time.Time     `json:"started_at"`
	LastAttemptAt                time.Time     `json:"last_attempt_at"`
	LastSuccessAt                time.Time     `json:"last_success_at"`
	LastEnumerationErrorCategory string        `json:"last_enumeration_error_category,omitempty"`
	LastEnumerationError         string        `json:"last_enumeration_error,omitempty"`
	Cycles                       int           `json:"cycles"`
	Observations                 int           `json:"observations"`
	Sessions                     int           `json:"sessions"`
	Degraded                     bool          `json:"degraded"`
}

type processKey struct {
	harness registry.Harness
	pid     int
	start   string
}
type trackedProcess struct {
	process      processinfo.Process
	missingSince time.Time
	missingCount int
}
type pendingScreenDecision struct {
	activity      registry.Activity
	ruleID        string
	confirmations uint8
}

type Observer struct {
	store                   Store
	interval                time.Duration
	grace                   time.Duration
	healthPath              string
	processList             ProcessLister
	paneList                mux.PaneLister
	catalogList             CatalogLister
	screenCapture           mux.ScreenCapturer
	disableScreenInspection bool
	manifestLoader          agentstate.Loader
	now                     func() time.Time
	errorWriter             io.Writer
	quiet                   bool

	mu              sync.Mutex
	startedAt       time.Time
	initialized     bool
	tracked         map[processKey]trackedProcess
	screenPending   map[processKey]pendingScreenDecision
	health          Health
	lastHealthWrite time.Time
	lockPath        string
	lockFile        *os.File
	running         bool
	continuous      bool
}

//nolint:cyclop // constructor applies defaults for each injectable observer dependency
func New(options Options) *Observer {
	providedStorePath := options.StorePath
	storePath := options.StorePath
	store := options.Store
	if store == nil {
		if storePath == "" {
			storePath = registry.DefaultStorePath()
		}
		store = registry.NewFileStore(storePath)
	} else if providedStorePath == "" {
		storePath = ""
	}
	processList := options.ProcessList
	if processList == nil {
		processList = processinfo.List
	}
	paneList := options.PaneList
	if paneList == nil {
		paneList = listMultiplexerPanes
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	interval := options.Interval
	if interval <= 0 {
		interval = defaultObserverInterval
	}
	errorWriter := options.ErrorWriter
	if errorWriter == nil {
		errorWriter = os.Stderr
	}
	healthPath := options.HealthPath
	if healthPath == "" && storePath != "" {
		healthPath = storePath + ".observer-health.json"
	}
	lockPath := ""
	if storePath != "" {
		lockPath = storePath + ".observer.lock"
	}
	catalogList := options.CatalogList
	if catalogList == nil {
		catalogList = DefaultCatalogList
	}
	screenCapture := options.ScreenCapture
	if screenCapture == nil {
		screenCapture = captureMultiplexerPane
	}
	return &Observer{
		store: store, interval: interval, grace: options.GracePeriod,
		healthPath: healthPath, processList: processList, paneList: paneList, catalogList: catalogList,
		screenCapture: screenCapture, manifestLoader: agentstate.Loader{ConfigDir: options.DetectionConfigDir},
		disableScreenInspection: options.DisableScreenInspection,
		now:                     now, errorWriter: errorWriter, quiet: options.Quiet,
		tracked: make(map[processKey]trackedProcess), screenPending: make(map[processKey]pendingScreenDecision),
		mu: sync.Mutex{}, startedAt: time.Time{}, initialized: false, health: Health{PID: 0, StartIdentity: "", Interval: 0, GracePeriod: 0, StartedAt: time.Time{}, LastAttemptAt: time.Time{}, LastSuccessAt: time.Time{}, LastEnumerationErrorCategory: "", LastEnumerationError: "", Cycles: 0, Observations: 0, Sessions: 0, Degraded: false},
		lastHealthWrite: time.Time{}, lockPath: lockPath, lockFile: nil, running: false, continuous: false,
	}
}

func (o *Observer) Run(ctx context.Context) error {
	return o.run(ctx, nil)
}

// RunWithResults runs the observer and calls handle after every reconciliation
// cycle. It is used by foreground clients that stream cycle results.
func (o *Observer) RunWithResults(ctx context.Context, handle func(Result) error) error {
	return o.run(ctx, handle)
}

//nolint:funcorder // shared run loop stays beside its two exported entrypoints
func (o *Observer) run(ctx context.Context, handle func(Result) error) error {
	if ctx == nil {
		return errObserverContextNil
	}
	if err := o.acquireLock(); err != nil {
		return err
	}
	defer o.releaseLock()
	o.continuous = true
	defer func() { o.continuous = false }()
	for {
		result, err := o.runCycle(ctx)
		if handle != nil {
			if handleErr := handle(result); handleErr != nil {
				return fmt.Errorf("handling observer result: %w", handleErr)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !o.quiet && o.errorWriter != nil {
				_, _ = fmt.Fprintf(o.errorWriter, "observer cycle failed: %v\n", err)
			}
		}
		timer := time.NewTimer(o.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (o *Observer) RunOnce(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, errObserverContextNil
	}
	if err := o.acquireLock(); err != nil {
		return Result{}, err
	}
	defer o.releaseLock()
	return o.runCycle(ctx)
}

//nolint:gocognit,cyclop,funcorder,maintidx // one cycle correlates process, multiplexer, catalog, and terminal evidence
func (o *Observer) runCycle(ctx context.Context) (Result, error) {
	at := o.now().UTC()
	result := Result{ObservedAt: at, Observations: 0, Sessions: 0, Processes: 0, Panes: 0, Catalog: 0, Present: 0, Gone: 0, Changed: 0, Degraded: false, Error: ""}
	if err := o.initializeTracked(ctx); err != nil {
		return o.failCycle(at, "registry", err, "initializing observer state", result)
	}
	processes, err := o.processList(ctx)
	if err != nil {
		return o.failCycle(at, "process-enumeration", err, "listing processes", result)
	}
	result.Processes = len(processes)
	panes, paneErr := o.paneList(ctx)
	if paneErr != nil {
		result.Degraded = true
		result.Error = paneErr.Error()
	}
	result.Panes = len(panes)
	sort.SliceStable(panes, func(left, right int) bool {
		return multiplexerPriority(panes[left].Location.Kind) > multiplexerPriority(panes[right].Location.Kind)
	})
	paneCommandCounts := commandPaneCounts(panes)
	catalog, catalogErr := o.listCatalog(ctx)
	if catalogErr != nil {
		result.Degraded = true
		result.Error = catalogErr.Error()
	}
	result.Catalog = len(catalog)
	knownSessions, sessionErr := o.store.List(ctx, registry.Filter{Harness: "", Presence: "", Activity: "", TmuxSession: "", MultiplexerSession: ""})
	if sessionErr != nil {
		return o.failCycle(at, "registry", sessionErr, "listing sessions for state detection", result)
	}

	catalogByPID := make(map[int]CatalogEntry)
	for _, entry := range catalog {
		if entry.Current && entry.ProcessPID > 0 {
			catalogByPID[entry.ProcessPID] = entry
		}
	}
	observations := make([]registry.Observation, 0, len(processes)+len(panes)+len(catalog))
	current := make(map[processKey]trackedProcess)
	processByPID := make(map[int]processinfo.Process, len(processes))
	harnessByPID := make(map[int]registry.Harness, len(processes))
	for _, process := range processes {
		if process.PID > 0 {
			processByPID[process.PID] = process
		}
	}
	for _, process := range processes {
		if process.PID <= 0 || process.StartIdentity == "" {
			continue
		}
		harnessID, ok := resolveHarness(process)
		if !ok {
			continue
		}
		harnessByPID[process.PID] = harnessID
	}
	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok || !isAgentWrapper(process) {
			continue
		}
		if _, descendantHarness, found := descendantHarnessProcess(process.PID, processes, processByPID, harnessByPID); found && descendantHarness == harnessID {
			delete(harnessByPID, process.PID)
		}
	}
	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok {
			continue
		}
		if hasAncestorHarness(process.PID, harnessID, processByPID, harnessByPID) {
			delete(harnessByPID, process.PID)
		}
	}
	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok {
			continue
		}
		key := processKey{harness: harnessID, pid: process.PID, start: process.StartIdentity}
		current[key] = trackedProcess{process: process, missingSince: time.Time{}, missingCount: 0}
		present := true
		identity := registry.ObservationIdentity{SessionID: "", SessionPath: ""}
		if entry, ok := catalogByPID[process.PID]; ok && entry.Harness == harnessID {
			identity = registry.ObservationIdentity{SessionID: entry.SessionID, SessionPath: entry.SessionPath}
		}
		observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // process evidence only
			Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
			Harness: harnessID, Identity: identity, ProcessPresent: &present, Process: processIdentity(process), ObservedAt: at,
		})
		result.Present++
	}
	locationPIDs := make(map[int]bool)
	for _, pane := range panes {
		process, harnessID, ok := multiplexerPaneProcess(pane, processes, processByPID, harnessByPID, paneCommandCounts)
		if !ok {
			continue
		}
		if locationPIDs[process.PID] {
			continue
		}
		location := pane.Location
		if location.PanePID == 0 && len(pane.Processes) > 0 {
			location.PanePID = pane.Processes[0].PID
		}
		if location.PaneTTY == "" {
			location.PaneTTY = pane.ProcessTTY
		}
		if location.Kind == registry.MultiplexerTmux {
			tmuxContext := location.TmuxContext()
			observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // tmux location only
				Source: registry.ObservationSourceTmux, Evidence: registry.ObservationEvidenceTmuxLocation,
				Harness: harnessID, Process: processIdentity(process), Tmux: &tmuxContext, ObservedAt: at,
			})
		} else {
			observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // multiplexer location only
				Source: registry.ObservationSourceMultiplexer, Evidence: registry.ObservationEvidenceMultiplexerLocation,
				Harness: harnessID, Process: processIdentity(process), Multiplexer: &location, ObservedAt: at,
			})
		}
		screenObservation, detected, detectErr := o.detectScreenState(ctx, knownSessions, harnessID, process, pane, at)
		if detected && o.screenDecisionReady(knownSessions, harnessID, process, screenObservation) {
			observations = append(observations, screenObservation)
		}
		if detectErr != nil {
			result.Degraded = true
			result.Error = detectErr.Error()
		}
		locationPIDs[process.PID] = true
	}
	if paneErr == nil {
		observations = append(observations, observationsForUnlocatedProcesses(o.manifestLoader, knownSessions, processByPID, harnessByPID, locationPIDs, at, !o.disableScreenInspection)...)
	}
	for _, entry := range catalog {
		if entry.Harness == "" || entry.SessionID == "" {
			continue
		}
		metadata := &registry.CatalogMetadata{ResumeCommand: append([]string(nil), entry.ResumeCommand...), CWD: entry.CWD, ProjectRoot: entry.ProjectRoot, ProcessPID: entry.ProcessPID, Current: entry.Current}
		observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // catalog metadata only
			Source: registry.ObservationSourceCatalog, Evidence: registry.ObservationEvidenceCatalogMetadata,
			Harness: entry.Harness, Identity: registry.ObservationIdentity{SessionID: entry.SessionID, SessionPath: entry.SessionPath},
			Catalog: metadata, ObservedAt: at,
		})
	}
	retiredKeys := make([]processKey, 0)
	nextTracked := make(map[processKey]trackedProcess, len(current)+len(o.tracked))
	maps.Copy(nextTracked, current)
	for key, old := range o.tracked {
		if _, ok := current[key]; ok {
			continue
		}
		old.missingCount++
		if old.missingSince.IsZero() {
			old.missingSince = at
		}
		eligible := o.grace > 0 && at.Sub(old.missingSince) >= o.grace
		if o.grace == 0 {
			eligible = old.missingCount >= defaultMissingSnapshots
		}
		if eligible {
			present := false
			observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // process absence only
				Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
				Harness: key.harness, ProcessPresent: &present, Process: processIdentity(old.process), ObservedAt: at,
			})
			result.Gone++
			nextTracked[key] = old
			retiredKeys = append(retiredKeys, key)
			continue
		}
		nextTracked[key] = old
	}
	absences := o.absenceObservationsForUnobservedSessions(knownSessions, processByPID, at)
	observations = append(observations, absences...)
	result.Gone += len(absences)
	o.prunePendingScreenDecisions(current)
	result.Observations = len(observations)
	if len(observations) > 0 {
		sessions, observeErr := o.observeBatch(ctx, observations)
		result.Sessions = len(sessions)
		result.Changed = len(sessions)
		if observeErr != nil {
			if errors.Is(observeErr, registry.ErrObservationConflict) {
				o.tracked = nextTracked
			}
			return o.failCycle(at, "registry", observeErr, "recording observations", result)
		}
	}
	for _, key := range retiredKeys {
		delete(nextTracked, key)
	}
	o.tracked = nextTracked
	if err := o.recordCycleHealth(at, result); err != nil {
		result.Degraded = true
		result.Error = err.Error()
		return result, fmt.Errorf("recording observer health: %w", err)
	}
	return result, nil
}

//nolint:funcorder // cycle failure finalization stays next to reconciliation
func (o *Observer) failCycle(at time.Time, component string, err error, wrapMsg string, result Result) (Result, error) {
	result.Degraded = true
	result.Error = err.Error()
	healthErr := o.recordHealth(at, true, component, err, result)
	return result, joinObserverHealthError(fmt.Errorf("%s: %w", wrapMsg, err), healthErr)
}

//nolint:funcorder // cycle health handling stays next to reconciliation
func (o *Observer) recordCycleHealth(at time.Time, result Result) error {
	if result.Degraded {
		return o.recordHealth(at, true, "reconciliation", fmt.Errorf("%w: %s", errObserverCycleDegraded, result.Error), result)
	}
	return o.recordHealth(at, false, "", nil, result)
}

func joinObserverHealthError(primary, healthErr error) error {
	if healthErr == nil {
		return primary
	}

	return errors.Join(primary, fmt.Errorf("recording observer health: %w", healthErr))
}

func resolveHarness(process processinfo.Process) (registry.Harness, bool) {
	if process.AgentHint != "" {
		if harnessID, err := harness.Normalize(process.AgentHint); err == nil {
			return observableHarness(process, harnessID)
		}
	}
	if harnessID, ok := harness.FromCommand(process.Executable); ok {
		return observableHarness(process, harnessID)
	}
	for _, arg := range process.Args[:min(commandArgumentPrefixCount, len(process.Args))] {
		if harnessID, ok := harness.FromCommand(arg); ok {
			return observableHarness(process, harnessID)
		}
	}
	if isAgentWrapper(process) {
		start := min(commandArgumentPrefixCount, len(process.Args))
		for _, arg := range process.Args[start:] {
			if harnessID, ok := harness.FromCommand(arg); ok {
				return observableHarness(process, harnessID)
			}
		}
	}
	return "", false
}

func observableHarness(process processinfo.Process, harnessID registry.Harness) (registry.Harness, bool) {
	if isTestFixtureProcess(process) {
		return "", false
	}
	for _, arg := range process.Args {
		if strings.HasPrefix(arg, "--type=") {
			return "", false
		}
	}
	if harnessID == registry.HarnessOmp {
		for _, arg := range process.Args {
			if strings.HasPrefix(filepath.Base(arg), "__omp_worker_") {
				return "", false
			}
		}
	}
	if harnessID == registry.HarnessCursor {
		if process.TTY == "" && process.MultiplexerPane == "" && !slices.Contains(process.Args, "agent") {
			return "", false
		}
	}
	return harnessID, true
}

func isTestFixtureProcess(process processinfo.Process) bool {
	if strings.Contains(process.Executable, "/aht-systest-") || strings.Contains(process.CWD, "/aht-systest-") {
		return true
	}
	for _, arg := range process.Args {
		if strings.Contains(arg, "/aht-systest-") {
			return true
		}
	}
	if slices.Contains(process.Args, "manage") && (slices.Contains(process.Args, "tracker") || slices.Contains(process.Args, "run")) {
		return true
	}
	return false
}

func hasAncestorHarness(pid int, harnessID registry.Harness, processByPID map[int]processinfo.Process, harnessByPID map[int]registry.Harness) bool {
	curr, ok := processByPID[pid]
	if !ok {
		return false
	}
	ppid := curr.PPID
	visited := map[int]bool{pid: true}
	for ppid > 0 && !visited[ppid] {
		visited[ppid] = true
		if ancestorHarness, found := harnessByPID[ppid]; found && ancestorHarness == harnessID {
			return true
		}
		parent, ok := processByPID[ppid]
		if !ok {
			break
		}
		ppid = parent.PPID
	}
	return false
}

func isAgentWrapper(process processinfo.Process) bool {
	command := filepath.Base(process.Executable)
	if command == "" && len(process.Args) > 0 {
		command = filepath.Base(process.Args[0])
	}
	switch command {
	case "env", "fence", "bwrap", "bubblewrap", "mise", "nix-shell", "nix", "direnv":
		return true
	default:
		return false
	}
}

func observationsForUnlocatedProcesses(manifestLoader agentstate.Loader, sessions []registry.Session, processByPID map[int]processinfo.Process, harnessByPID map[int]registry.Harness, locationPIDs map[int]bool, at time.Time, inspectScreen bool) []registry.Observation {
	observations := make([]registry.Observation, 0, len(harnessByPID))
	for pid, harnessID := range harnessByPID {
		if locationPIDs[pid] {
			continue
		}
		process, ok := processByPID[pid]
		if !ok {
			continue
		}
		emptyContext := registry.MultiplexerContext{}             //nolint:exhaustruct_v5 // zero value means no multiplexer pane
		observations = append(observations, registry.Observation{ //nolint:exhaustruct_v5 // location evidence only
			Source: registry.ObservationSourceMultiplexer, Evidence: registry.ObservationEvidenceMultiplexerLocation,
			Harness: harnessID, Process: processIdentity(process), Multiplexer: &emptyContext, ObservedAt: at,
		})
		if !inspectScreen {
			continue
		}
		if screenObservation, detected := unavailableScreenState(manifestLoader, sessions, harnessID, process, at, "screen_not_in_supported_multiplexer"); detected {
			observations = append(observations, screenObservation)
		}
	}
	return observations
}

func unobservedSessionAbsence(session registry.Session, processByPID map[int]processinfo.Process, at time.Time) (registry.Observation, bool) {
	var empty registry.Observation
	if session.Process != nil && session.Process.Complete() {
		if process, ok := processByPID[session.Process.PID]; ok && (process.StartIdentity == "" || process.StartIdentity == session.Process.StartIdentity) {
			return empty, false
		}
		present := false
		return registry.Observation{ //nolint:exhaustruct_v5 // process absence only
			Source:         registry.ObservationSourceProcess,
			Evidence:       registry.ObservationEvidenceProcessPresence,
			Harness:        session.Harness,
			ProcessPresent: &present,
			//nolint:exhaustruct_v5 // PID and start identity suffice
			Process: &registry.ProcessIdentity{
				PID:           session.Process.PID,
				StartIdentity: session.Process.StartIdentity,
			},
			ObservedAt: at,
		}, true
	}

	panePID := session.Multiplexer.PanePID
	if panePID == 0 {
		panePID = session.Tmux.PanePID
	}
	if session.Process == nil && panePID > 0 {
		if _, ok := processByPID[panePID]; ok {
			return empty, false
		}

		present := false
		return registry.Observation{ //nolint:exhaustruct_v5 // process absence only
			Source:         registry.ObservationSourceProcess,
			Evidence:       registry.ObservationEvidenceProcessPresence,
			Harness:        session.Harness,
			Identity:       registry.ObservationIdentity{SessionID: session.SessionID, SessionPath: session.SessionPath},
			ProcessPresent: &present,
			ObservedAt:     at,
		}, true
	}

	return empty, false
}

func sessionForProcess(sessions []registry.Session, harnessID registry.Harness, identity *registry.ProcessIdentity) registry.Session {
	session := registry.Session{ //nolint:exhaustruct_v5 // policy inputs only
		Harness: harnessID,
		Process: identity,
	}
	for _, candidate := range sessions {
		if candidate.Harness == harnessID && candidate.Process != nil && candidate.Process.Equal(*identity) {
			return candidate
		}
	}
	return session
}

func screenFallbackMetadata(session registry.Session, harnessID registry.Harness, at time.Time) (string, string) {
	policy := agentstate.PolicyFor(harnessID)
	if policy.Primary != agentstate.AuthorityHook {
		return "", ""
	}
	return policy.IntegrationValue, agentstate.EvaluateHook(session, at).Reason
}

func unavailableScreenState(manifestLoader agentstate.Loader, sessions []registry.Session, harnessID registry.Harness, process processinfo.Process, at time.Time, reason string) (registry.Observation, bool) {
	if !manifestLoader.Supports(harnessID) {
		var empty registry.Observation
		return empty, false
	}
	identity := processIdentity(process)
	session := sessionForProcess(sessions, harnessID, identity)
	if !shouldDetectScreen(session, at) {
		var empty registry.Observation
		return empty, false
	}
	fallback, fallbackReason := screenFallbackMetadata(session, harnessID, at)
	unknown := registry.ActivityUnknown
	screen := &registry.ScreenObservation{Activity: unknown, Authority: string(agentstate.AuthorityScreen), Reason: reason, RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackForIntegration: fallback, FallbackReason: fallbackReason, Process: *identity, ObservedAt: at}
	observation := registry.Observation{ //nolint:exhaustruct_v5 // no terminal data available
		Source: registry.ObservationSourceScreen, Evidence: registry.ObservationEvidenceScreenState, Harness: harnessID,
		Activity: &unknown, Process: identity, Screen: screen, ObservedAt: at,
	}
	return observation, true
}

//nolint:funcorder // state detection runs as part of reconciliation near its call site
func (o *Observer) detectScreenState(ctx context.Context, sessions []registry.Session, harnessID registry.Harness, process processinfo.Process, pane mux.Pane, at time.Time) (registry.Observation, bool, error) {
	if !o.manifestLoader.Supports(harnessID) {
		var empty registry.Observation
		return empty, false, nil
	}
	identity := processIdentity(process)
	session := sessionForProcess(sessions, harnessID, identity)
	if !shouldDetectScreen(session, at) {
		var empty registry.Observation
		return empty, false, nil
	}
	if pane.Activity != nil {
		fallback, fallbackReason := screenFallbackMetadata(session, harnessID, at)
		reason := pane.StateReason
		if reason == "" {
			reason = "multiplexer_agent_status"
		}
		screen := &registry.ScreenObservation{
			Activity: *pane.Activity, Authority: string(pane.Location.Kind), Reason: reason,
			RuleID: "", ManifestSource: "", ManifestVersion: 0,
			FallbackForIntegration: fallback, FallbackReason: fallbackReason,
			Process: *identity, ObservedAt: at,
		}
		observation := registry.Observation{ //nolint:exhaustruct_v5 // semantic state omits terminal contents
			Source: registry.ObservationSourceScreen, Evidence: registry.ObservationEvidenceScreenState, Harness: harnessID,
			Activity: pane.Activity, Process: identity, Screen: screen, ObservedAt: at,
		}
		return observation, true, nil
	}
	if o.disableScreenInspection {
		var empty registry.Observation
		return empty, false, nil
	}
	return o.captureScreenState(ctx, session, harnessID, identity, pane, at)
}

//nolint:funcorder // screen stabilization stays beside screen detection.
func (o *Observer) screenDecisionReady(
	sessions []registry.Session,
	harnessID registry.Harness,
	process processinfo.Process,
	observation registry.Observation,
) bool {
	if !o.continuous ||
		observation.Screen == nil ||
		observation.Activity == nil ||
		observation.Screen.Authority != string(agentstate.AuthorityScreen) {
		return true
	}

	identity := processIdentity(process)
	session := sessionForProcess(sessions, harnessID, identity)
	if session.Observations.Screen != nil &&
		session.Observations.Screen.Process.Equal(*identity) {
		delete(o.screenPending, processKey{
			harness: harnessID,
			pid:     process.PID,
			start:   process.StartIdentity,
		})

		return true
	}

	key := processKey{harness: harnessID, pid: process.PID, start: process.StartIdentity}
	pending := o.screenPending[key]
	if pending.activity != *observation.Activity ||
		pending.ruleID != observation.Screen.RuleID {
		o.screenPending[key] = pendingScreenDecision{
			activity:      *observation.Activity,
			ruleID:        observation.Screen.RuleID,
			confirmations: 1,
		}

		return false
	}

	pending.confirmations++
	if pending.confirmations < initialScreenConfirmations {
		o.screenPending[key] = pending

		return false
	}

	delete(o.screenPending, key)

	return true
}

//nolint:funcorder // screen stabilization stays beside screen detection.
func (o *Observer) prunePendingScreenDecisions(current map[processKey]trackedProcess) {
	for key := range o.screenPending {
		if _, ok := current[key]; !ok {
			delete(o.screenPending, key)
		}
	}
}

func shouldDetectScreen(session registry.Session, at time.Time) bool {
	if agentstate.SupportsScreen(session.Harness) {
		return agentstate.ShouldDetectScreen(session, at)
	}
	policy := agentstate.PolicyFor(session.Harness)
	return policy.Primary == agentstate.AuthorityHook && !agentstate.HookIsActive(session, at)
}

func screenObservationTime(cycleAt time.Time, capturedAt time.Time) time.Time {
	if capturedAt.Before(cycleAt) {
		return cycleAt
	}
	return capturedAt
}

func preferForegroundProcess(candidate processinfo.Process, current processinfo.Process) bool {
	candidateDirect := isDirectAgentProcess(candidate)
	currentDirect := isDirectAgentProcess(current)
	if candidateDirect != currentDirect {
		return candidateDirect
	}
	candidateLeader := candidate.PID == candidate.ProcessGroupID
	currentLeader := current.PID == current.ProcessGroupID
	if candidateLeader != currentLeader {
		return candidateLeader
	}
	return candidate.PID < current.PID
}

func isDirectAgentProcess(process processinfo.Process) bool {
	if isAgentWrapper(process) {
		return false
	}
	if _, ok := harness.FromCommand(process.Executable); ok {
		return true
	}
	for _, arg := range process.Args[:min(commandArgumentPrefixCount, len(process.Args))] {
		if _, ok := harness.FromCommand(arg); ok {
			return true
		}
	}
	return false
}

func processIdentity(process processinfo.Process) *registry.ProcessIdentity {
	return &registry.ProcessIdentity{PID: process.PID, PPID: process.PPID, ProcessGroupID: process.ProcessGroupID, Foreground: process.Foreground, StartIdentity: process.StartIdentity, Executable: process.Executable, CWD: process.CWD, TTY: process.TTY}
}

//nolint:funcorder // private helpers are grouped with lock and reconciliation internals
func (o *Observer) initializeTracked(ctx context.Context) error {
	if o.initialized {
		return nil
	}
	sessions, err := o.store.List(ctx, registry.Filter{Harness: "", Presence: registry.PresenceLive, Activity: "", TmuxSession: "", MultiplexerSession: ""})
	if err != nil {
		return fmt.Errorf("listing live sessions: %w", err)
	}
	for _, session := range sessions {
		observation := session.Observations.Process
		if observation == nil || !observation.Present || !observation.Process.Complete() {
			continue
		}
		process := processinfo.Process{
			PID:             observation.Process.PID,
			PPID:            observation.Process.PPID,
			ProcessGroupID:  observation.Process.ProcessGroupID,
			Foreground:      observation.Process.Foreground,
			StartIdentity:   observation.Process.StartIdentity,
			Executable:      observation.Process.Executable,
			CWD:             observation.Process.CWD,
			TTY:             observation.Process.TTY,
			AgentHint:       "",
			MultiplexerKind: "", MultiplexerServer: "", MultiplexerSession: "", MultiplexerPane: "",
			Args: nil,
		}
		key := processKey{harness: session.Harness, pid: process.PID, start: process.StartIdentity}
		o.tracked[key] = trackedProcess{
			process:      process,
			missingSince: observation.ObservedAt,
			missingCount: defaultMissingSnapshots - 1,
		}
	}
	o.initialized = true
	return nil
}

//nolint:funcorder // private helpers are grouped with lock and reconciliation internals
func (o *Observer) listCatalog(ctx context.Context) ([]CatalogEntry, error) {
	if o.catalogList == nil {
		return nil, nil
	}
	return o.catalogList(ctx)
}

//nolint:funcorder // private helpers are grouped with lock and reconciliation internals
func (o *Observer) acquireLock() error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return ErrAlreadyRunning
	}
	o.running = true
	o.mu.Unlock()

	if o.lockPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(o.lockPath), 0o700); err != nil {
		o.clearRunning()
		return fmt.Errorf("create observer lock directory: %w", err)
	}
	file, err := openObserverLock(o.lockPath)
	if err != nil {
		o.clearRunning()
		return fmt.Errorf("observer already running or lock unavailable: %w", err)
	}
	o.mu.Lock()
	o.lockFile = file
	o.mu.Unlock()
	return nil
}

//nolint:funcorder // private helpers are grouped with lock and reconciliation internals
func (o *Observer) releaseLock() {
	o.mu.Lock()
	file := o.lockFile
	o.lockFile = nil
	o.running = false
	o.mu.Unlock()
	if file == nil {
		return
	}
	_ = closeObserverLock(file)
	_ = removeObserverLock(o.lockPath)
}

//nolint:cyclop,funcorder // health persistence handles degraded categories and atomic writes
func (o *Observer) recordHealth(at time.Time, degraded bool, category string, err error, result Result) error {
	o.mu.Lock()
	wasDegraded := o.health.Degraded
	if o.startedAt.IsZero() {
		o.startedAt = at
		o.health.PID = os.Getpid()
		o.health.StartedAt = at
		o.health.Interval = o.interval
		o.health.GracePeriod = o.grace
	}
	o.health.LastAttemptAt = at
	o.health.Cycles++
	o.health.Observations += result.Observations
	o.health.Sessions += result.Sessions
	o.health.Degraded = degraded
	if err != nil {
		o.health.LastEnumerationErrorCategory = category
		o.health.LastEnumerationError = err.Error()
	} else if !degraded {
		o.health.LastSuccessAt = at
		o.health.LastEnumerationErrorCategory = ""
		o.health.LastEnumerationError = ""
	}
	health := o.health
	shouldWrite := o.lastHealthWrite.IsZero() || at.Sub(o.lastHealthWrite) >= 30*time.Second || err != nil || degraded != wasDegraded
	o.mu.Unlock()
	if !shouldWrite || o.healthPath == "" {
		return nil
	}
	data, marshalErr := json.MarshalIndent(health, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("encoding observer health: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(o.healthPath), 0o700); err != nil {
		return fmt.Errorf("creating observer health directory: %w", err)
	}
	tmp := o.healthPath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing observer health: %w", err)
	}
	if err := os.Rename(tmp, o.healthPath); err != nil {
		cleanupErr := os.Remove(tmp)
		if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			cleanupErr = fmt.Errorf("removing temporary observer health file: %w", cleanupErr)
		} else {
			cleanupErr = nil
		}

		return errors.Join(fmt.Errorf("publishing observer health: %w", err), cleanupErr)
	}
	o.mu.Lock()
	o.lastHealthWrite = at
	o.mu.Unlock()

	return nil
}

func (o *Observer) Health() Health {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.health
}

func (o *Observer) isProcessTracked(session registry.Session) bool {
	if o == nil || session.Process == nil {
		return false
	}
	for key := range o.tracked {
		if key.harness == session.Harness && key.pid == session.Process.PID {
			if key.start == "" || session.Process.StartIdentity == "" || key.start == session.Process.StartIdentity {
				return true
			}
		}
	}
	return false
}

// absenceObservationsForUnobservedSessions retires nonterminal registry sessions whose
// recorded process identity no longer exists and is not covered by the tracked-process
// sweep, including sessions revived by lifecycle hooks after a process absence.
func (o *Observer) absenceObservationsForUnobservedSessions(sessions []registry.Session, processByPID map[int]processinfo.Process, at time.Time) []registry.Observation {
	observations := make([]registry.Observation, 0)
	for _, session := range sessions {
		if session.Presence == registry.PresenceGone {
			continue
		}
		if o.isProcessTracked(session) {
			continue
		}

		if observation, ok := unobservedSessionAbsence(session, processByPID, at); ok {
			observations = append(observations, observation)
		}
	}

	return observations
}

func (o *Observer) captureScreenState(ctx context.Context, session registry.Session, harnessID registry.Harness, identity *registry.ProcessIdentity, pane mux.Pane, at time.Time) (registry.Observation, bool, error) {
	manifest, err := o.manifestLoader.Load(harnessID)
	if err != nil {
		return registry.Observation{}, false, fmt.Errorf("loading %s detection manifest: %w", harnessID, err)
	}
	snapshot, captureErr := o.screenCapture(ctx, pane)
	if captureErr != nil {
		return registry.Observation{}, false, fmt.Errorf("capturing %s pane %s for detection: %w", harnessID, pane.Location.PaneID, captureErr)
	}
	decision := manifest.Evaluate(agentstate.NormalizeSnapshot(snapshot.Text, snapshot.Title))
	if decision.Activity == registry.ActivityUnknown &&
		decision.Reason == "no_rule_matched" &&
		session.Activity != nil &&
		*session.Activity != registry.ActivityUnknown {
		var empty registry.Observation
		return empty, false, nil
	}
	observedAt := screenObservationTime(at, o.now().UTC())
	fallback, fallbackReason := screenFallbackMetadata(session, harnessID, at)
	screen := &registry.ScreenObservation{Activity: decision.Activity, Authority: string(agentstate.AuthorityScreen), Reason: decision.Reason, RuleID: decision.RuleID, ManifestSource: decision.ManifestSource, ManifestVersion: decision.ManifestVersion, FallbackForIntegration: fallback, FallbackReason: fallbackReason, Process: *identity, ObservedAt: observedAt}
	observation := registry.Observation{ //nolint:exhaustruct_v5 // screen evidence omits terminal contents
		Source: registry.ObservationSourceScreen, Evidence: registry.ObservationEvidenceScreenState, Harness: harnessID,
		Activity: &decision.Activity, Process: identity, Screen: screen, ObservedAt: observedAt,
	}
	if manifest.Warning != "" {
		return observation, true, fmt.Errorf("%w: %s", errDetectionOverrideInvalid, manifest.Warning)
	}
	return observation, true, nil
}

func (o *Observer) observeBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error) {
	sessions, err := o.store.ObserveBatch(ctx, observations)
	if !errors.Is(err, registry.ErrObservationConflict) {
		if err != nil {
			return sessions, fmt.Errorf("recording observation batch: %w", err)
		}

		return sessions, nil
	}

	sessions = make([]registry.Session, 0, len(observations))
	observationErrs := []error{fmt.Errorf("atomic observation batch: %w", err)}
	for index, observation := range observations {
		session, observeErr := o.store.Observe(ctx, observation)
		if observeErr != nil {
			observationErrs = append(observationErrs, fmt.Errorf("observation %d: %w", index, observeErr))
			continue
		}
		sessions = append(sessions, session)
	}

	return sessions, errors.Join(observationErrs...)
}

func (o *Observer) clearRunning() {
	o.mu.Lock()
	o.running = false
	o.mu.Unlock()
}

func (r Result) String() string {
	result := fmt.Sprintf(
		"observations=%d sessions=%d processes=%d panes=%d catalog=%d present=%d gone=%d changed=%d degraded=%t",
		r.Observations, r.Sessions, r.Processes, r.Panes, r.Catalog, r.Present, r.Gone, r.Changed, r.Degraded,
	)
	if r.Error != "" {
		result += " error=" + strconv.Quote(r.Error)
	}
	return result
}
