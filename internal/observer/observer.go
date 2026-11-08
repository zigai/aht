package observer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultObserverInterval = time.Second
	defaultMissingSnapshots = 2
	// defaultProcessConfirmation is how long a process found only by command
	// name must keep running before it becomes a registry session.
	defaultProcessConfirmation = 3 * time.Second
	// healthWriteInterval bounds how stale the health file may get while the
	// observer status and error category stay unchanged.
	healthWriteInterval        = 30 * time.Second
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
	Harness       registry.Harness  `json:"harness"`
	SessionID     string            `json:"session_id,omitempty"`
	SessionPath   string            `json:"session_path,omitempty"`
	ResumeCommand []string          `json:"resume_command,omitempty"`
	CWD           string            `json:"cwd,omitempty"`
	ProjectRoot   string            `json:"project_root,omitempty"`
	ProcessPID    int               `json:"process_pid,omitempty"`
	Current       bool              `json:"current"`
	Activity      registry.Activity `json:"activity,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Title         string            `json:"title,omitempty"`
	Reporter      registry.Reporter `json:"-"`
}
type CatalogLister func(context.Context) ([]CatalogEntry, error)

// Store is the observation and inventory capability required by an Observer.
type Store interface {
	Observe(ctx context.Context, observation registry.Observation) (registry.Session, error)
	ObserveBatch(
		ctx context.Context,
		observations []registry.Observation,
	) ([]registry.Session, error)
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
}

//exhaustruct:optional
type Options struct {
	Store            Store
	StorePath        string
	Interval         time.Duration
	GracePeriod      time.Duration
	ProcessList      ProcessLister
	PaneList         mux.PaneLister
	LocationPresence func(context.Context, registry.Location) (registry.Presence, error)
	CatalogList      CatalogLister
	ScreenCapture    mux.ScreenCapturer
	// DisableScreenInspection disables terminal capture and screen-derived state.
	// Native multiplexer activity remains available without reading screen text.
	DisableScreenInspection bool
	DetectionConfigDir      string
	// ProcessConfirmation is how long a continuously running observer waits
	// before recording a process discovered only by its command name. Native
	// reports, current catalog entries, and existing sessions confirm a process
	// at once. Zero uses a 3 second default; a negative value confirms at once.
	// Single-cycle runs confirm at once because they have no history.
	ProcessConfirmation time.Duration
	Now                 func() time.Time
	ErrorWriter         io.Writer
	Quiet               bool
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

type cycleInventory struct {
	processes     []processinfo.Process
	processByPID  map[int]processinfo.Process
	tree          *processTree
	harnessByPID  map[int]registry.Harness
	knownSessions []registry.Session
}

type trackedUpdate struct {
	next       map[processKey]trackedProcess
	retired    []processKey
	candidates map[processKey]time.Time
}

type Observer struct {
	store                   Store
	interval                time.Duration
	grace                   time.Duration
	healthPath              string
	processList             ProcessLister
	paneList                func(context.Context, []processinfo.Process) ([]mux.Pane, error)
	locationPresence        func(context.Context, registry.Location) (registry.Presence, error)
	catalogList             CatalogLister
	screenCapture           mux.ScreenCapturer
	disableScreenInspection bool
	manifestLoader          agentstate.Loader
	now                     func() time.Time
	errorWriter             io.Writer
	quiet                   bool
	confirmAfter            time.Duration

	//exhaustruct:optional
	mu              sync.Mutex
	startedAt       time.Time
	initialized     bool
	tracked         map[processKey]trackedProcess
	candidates      map[processKey]time.Time
	screenPending   map[processKey]pendingScreenDecision
	health          Health
	lastHealthWrite time.Time
	lockPath        string
	lockFile        *os.File
	running         bool
	continuous      bool
}

func New(opts Options) *Observer {
	store, storePath := observerStore(opts)

	processList := opts.ProcessList
	if processList == nil {
		processList = func(ctx context.Context) ([]processinfo.Process, error) {
			return processinfo.ListWithEnv(ctx, append(drivers.EnvKeys(), processinfo.ChildRoleEnv))
		}
	}

	paneList := listMultiplexerPanes
	if opts.PaneList != nil {
		paneList = func(ctx context.Context, _ []processinfo.Process) ([]mux.Pane, error) { return opts.PaneList(ctx) }
	}

	checkLocation := opts.LocationPresence
	if checkLocation == nil {
		checkLocation = locationPresence
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = defaultObserverInterval
	}

	errorWriter := opts.ErrorWriter
	if errorWriter == nil {
		errorWriter = os.Stderr
	}

	var healthPath, observerLockPath string
	if storePath != "" {
		healthPath, observerLockPath = HealthPath(storePath), lockPath(storePath)
	}

	catalogList := opts.CatalogList
	if catalogList == nil {
		catalogList = DefaultCatalogList
	}

	screenCapture := opts.ScreenCapture
	if screenCapture == nil {
		screenCapture = captureMultiplexerPane
	}

	confirmAfter := opts.ProcessConfirmation
	if confirmAfter == 0 {
		confirmAfter = defaultProcessConfirmation
	}

	return &Observer{
		store:                   store,
		interval:                interval,
		grace:                   opts.GracePeriod,
		healthPath:              healthPath,
		processList:             processList,
		paneList:                paneList,
		locationPresence:        checkLocation,
		catalogList:             catalogList,
		screenCapture:           screenCapture,
		manifestLoader:          agentstate.Loader{ConfigDir: opts.DetectionConfigDir},
		disableScreenInspection: opts.DisableScreenInspection,
		now:                     now,
		errorWriter:             errorWriter,
		quiet:                   opts.Quiet,
		confirmAfter:            confirmAfter,
		tracked: make(
			map[processKey]trackedProcess,
		),
		candidates:    make(map[processKey]time.Time),
		screenPending: make(map[processKey]pendingScreenDecision),
		startedAt:     time.Time{},
		initialized:   false,
		health: Health{
			PID:                          0,
			Interval:                     0,
			GracePeriod:                  0,
			StartedAt:                    time.Time{},
			LastAttemptAt:                time.Time{},
			LastSuccessAt:                time.Time{},
			LastEnumerationErrorCategory: "",
			LastEnumerationError:         "",
			Cycles:                       0,
			Observations:                 0,
			Sessions:                     0,
			Degraded:                     false,
		},
		lastHealthWrite: time.Time{},
		lockPath:        observerLockPath,
		lockFile:        nil,
		running:         false,
		continuous:      false,
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

func (o *Observer) RunOnce(ctx context.Context) (_ Result, err error) {
	if ctx == nil {
		return Result{}, errObserverContextNil
	}

	if err := o.acquireLock(); err != nil {
		return Result{}, err
	}
	defer func() { err = errors.Join(err, o.releaseLock()) }()

	return o.runCycle(ctx)
}

func (o *Observer) Health() Health {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.health
}

func (o *Observer) run(ctx context.Context, handle func(Result) error) (err error) {
	if ctx == nil {
		return errObserverContextNil
	}

	if err := o.acquireLock(); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, o.releaseLock()) }()

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

func (o *Observer) runCycle(ctx context.Context) (Result, error) {
	at := o.now().UTC()

	result := Result{
		ObservedAt:   at,
		Observations: 0,
		Sessions:     0,
		Processes:    0,
		Panes:        0,
		Catalog:      0,
		Present:      0,
		Gone:         0,
		Changed:      0,
		Degraded:     false,
		Error:        "",
	}
	if err := o.initializeTracked(ctx); err != nil {
		return o.failCycle(at, "registry", err, "initializing observer state", result)
	}

	processes, err := o.processList(ctx)
	if err != nil {
		return o.failCycle(at, "process-enumeration", err, "listing processes", result)
	}

	processes = slices.Clone(processes)
	result.Processes = len(processes)

	panes, paneErr := o.paneList(ctx, processes)
	if paneErr != nil {
		result = result.withDegradation(paneErr)
	}

	result.Panes = len(panes)
	sortPanesByPriority(panes)

	catalogEntries, catalogErr := o.catalogList(ctx)
	if catalogErr != nil {
		result = result.withDegradation(catalogErr)
	}

	result.Catalog = len(catalogEntries)

	knownSessions, sessionErr := o.store.List(ctx, registry.Filter{})
	if sessionErr != nil {
		return o.failCycle(
			at,
			"registry",
			sessionErr,
			"listing sessions for state detection",
			result,
		)
	}

	observations := make([]registry.Observation, 0, len(processes)+len(panes)+len(catalogEntries))
	processByPID := processesByPID(processes)
	tree := newProcessTree(processByPID)
	catalogEntries, catalogByPID := claimCurrentCatalogEntries(catalogEntries, processByPID)
	harnessByPID := resolveHarnesses(processes)
	pruneNestedHarnesses(processes, tree, harnessByPID)
	normalizeHarnessForeground(processes, tree, harnessByPID)

	candidates := o.holdUnconfirmedProcesses(
		knownSessions,
		catalogByPID,
		processByPID,
		harnessByPID,
		at,
	)

	inventory := cycleInventory{
		processes:    processes,
		processByPID: processByPID, tree: tree,
		harnessByPID:  harnessByPID,
		knownSessions: knownSessions,
	}

	sightings, current := processSightings(inventory, catalogByPID, at)
	observations = append(observations, sightings...)
	result.Present += len(sightings)

	placements, locationPIDs, detectErr := o.paneObservations(ctx, inventory, panes, at)
	observations = append(observations, placements...)

	if detectErr != nil {
		result = result.withDegradation(detectErr)
	}

	if paneErr == nil {
		observations = append(
			observations,
			observationsForUnlocatedProcesses(
				o.manifestLoader,
				knownSessions,
				processByPID,
				harnessByPID,
				locationPIDs,
				at,
				!o.disableScreenInspection,
			)...,
		)
	}

	observations = append(observations, catalogObservations(catalogEntries, processByPID, at)...)

	gone, update := o.retireMissingProcesses(current, at)
	update.candidates = candidates

	observations = append(observations, gone...)
	result.Gone += len(gone)

	reconciled, reconcileErr := o.reconcileUnobservedSessions(ctx, knownSessions, processByPID, at)

	observations = append(observations, reconciled...)
	result.Gone += goneVerificationCount(reconciled)

	if reconcileErr != nil {
		result = result.withDegradation(reconcileErr)
	}

	o.prunePendingScreenDecisions(current)

	return o.recordObservations(ctx, at, observations, update, result)
}

func (o *Observer) paneObservations(
	ctx context.Context,
	inventory cycleInventory,
	panes []mux.Pane,
	at time.Time,
) ([]registry.Observation, map[int]bool, error) {
	observations := make([]registry.Observation, 0, len(panes))
	locationPIDs := make(map[int]bool)
	paneCommandCounts := commandPaneCounts(panes)

	var detectErr error

	for _, pane := range panes {
		process, harnessID, ok := multiplexerPaneProcess(
			pane,
			inventory.processes,
			inventory.tree,
			inventory.harnessByPID,
			paneCommandCounts,
		)

		claimed := locationPIDs[process.PID]
		observations = append(
			observations,
			panePlacementObservations(inventory, pane, process, ok, locationPIDs, at)...,
		)

		if !ok || claimed ||
			nestedHarnessProcess(process, inventory.tree, inventory.harnessByPID) {
			continue
		}
		// Only infer a foreground owner when controlling-TTY job-control metadata is unavailable.
		if !process.Foreground && ((process.ProcessGroupID > 0 && process.TTY != "") ||
			paneForegroundKnown(pane, inventory.processes)) {
			continue
		}

		screenObservation, detected, err := o.detectScreenState(
			ctx,
			inventory.knownSessions,
			harnessID,
			process,
			pane,
			at,
		)
		if detected &&
			o.screenDecisionReady(inventory.knownSessions, harnessID, process, screenObservation) {
			observations = append(observations, screenObservation)
		}

		if err != nil {
			detectErr = err
		}
	}

	return observations, locationPIDs, detectErr
}

func (o *Observer) retireMissingProcesses(
	current map[processKey]trackedProcess,
	at time.Time,
) ([]registry.Observation, trackedUpdate) {
	observations := make([]registry.Observation, 0)
	update := trackedUpdate{
		next:       make(map[processKey]trackedProcess, len(current)+len(o.tracked)),
		retired:    make([]processKey, 0),
		candidates: nil,
	}
	maps.Copy(update.next, current)

	for key, old := range o.tracked {
		if _, ok := current[key]; ok {
			continue
		}

		old.missingCount++
		if old.missingSince.IsZero() {
			old.missingSince = at
		}

		update.next[key] = old

		if !o.missingProcessRetired(old, at) {
			continue
		}

		observations = append(
			observations,
			processObservation(key.harness, at, &registry.Sighting{
				Process: *processIdentity(old.process),
				Present: false,
			}),
		)
		update.retired = append(update.retired, key)
	}

	return observations, update
}

func (o *Observer) missingProcessRetired(process trackedProcess, at time.Time) bool {
	if o.grace == 0 {
		return process.missingCount >= defaultMissingSnapshots
	}

	return o.grace > 0 && at.Sub(process.missingSince) >= o.grace
}

func (o *Observer) recordObservations(
	ctx context.Context,
	at time.Time,
	observations []registry.Observation,
	update trackedUpdate,
	result Result,
) (Result, error) {
	result.Observations = len(observations)
	if len(observations) > 0 {
		sessions, observeErr := o.observeBatch(ctx, observations)
		result.Sessions = len(sessions)
		result.Changed = len(sessions)

		if observeErr != nil {
			if errors.Is(observeErr, registry.ErrObservationConflict) {
				o.tracked = update.next
			}

			return o.failCycle(at, "registry", observeErr, "recording observations", result)
		}
	}

	for _, key := range update.retired {
		delete(update.next, key)
	}

	o.tracked = update.next

	o.candidates = update.candidates
	if err := o.recordCycleHealth(at, result); err != nil {
		result = result.withDegradation(err)

		return result, fmt.Errorf("recording observer health: %w", err)
	}

	return result, nil
}

func (o *Observer) failCycle(
	at time.Time,
	component string,
	err error,
	wrapMsg string,
	result Result,
) (Result, error) {
	result.Degraded = true
	result.Error = err.Error()
	healthErr := o.recordHealth(at, true, component, err, result)

	return result, joinObserverHealthError(fmt.Errorf("%s: %w", wrapMsg, err), healthErr)
}

func (o *Observer) recordCycleHealth(at time.Time, result Result) error {
	if result.Degraded {
		return o.recordHealth(
			at,
			true,
			"reconciliation",
			fmt.Errorf("%w: %s", errObserverCycleDegraded, result.Error),
			result,
		)
	}

	return o.recordHealth(at, false, "", nil, result)
}

func observerStore(opts Options) (Store, string) {
	if opts.Store != nil {
		return opts.Store, opts.StorePath
	}

	storePath := opts.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}

	return registry.NewJournal(storePath, catalog.Rules{}), storePath
}

// An agent can itself look like a wrapper (pi runs as node), so only a chain of
// wrappers counts as launching the descendant; agents nested behind other tools
// belong to the outer agent.
func launchesHarnessThroughWrappers(
	wrapperPID int,
	harnessID registry.Harness,
	processes []processinfo.Process,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) bool {
	for _, process := range processes {
		if process.PID == wrapperPID || harnessByPID[process.PID] != harnessID {
			continue
		}

		for ppid := range tree.parents(process.PID) {
			if ppid == wrapperPID {
				return true
			}

			parent, ok := tree.byPID[ppid]
			if !ok || !catalog.IsWrapper(parent) {
				break
			}
		}
	}

	return false
}

func joinObserverHealthError(primary error, healthErr error) error {
	if healthErr == nil {
		return primary
	}

	return errors.Join(primary, fmt.Errorf("recording observer health: %w", healthErr))
}

func sortPanesByPriority(panes []mux.Pane) {
	slices.SortStableFunc(panes, func(left, right mux.Pane) int {
		return cmp.Compare(
			drivers.Precedence(right.Location.Kind),
			drivers.Precedence(left.Location.Kind),
		)
	})
}

func processesByPID(processes []processinfo.Process) map[int]processinfo.Process {
	processByPID := make(map[int]processinfo.Process, len(processes))
	for _, process := range processes {
		if process.PID > 0 {
			processByPID[process.PID] = process
		}
	}

	return processByPID
}

func claimCurrentCatalogEntries(
	entries []CatalogEntry,
	processByPID map[int]processinfo.Process,
) ([]CatalogEntry, map[int]CatalogEntry) {
	catalogByPID := make(map[int]CatalogEntry)

	entries = slices.Clone(entries)
	for index, entry := range entries {
		if !entry.Current || entry.ProcessPID <= 0 {
			continue
		}

		if _, ok := catalogListedProcess(entry, processByPID); !ok {
			// Keep the saved catalog identity and metadata, but not a stale PID claim.
			entries[index].Current = false
			entries[index].ProcessPID = 0

			continue
		}

		catalogByPID[entry.ProcessPID] = entry
	}

	return entries, catalogByPID
}

func resolveHarnesses(processes []processinfo.Process) map[int]registry.Harness {
	harnessByPID := make(map[int]registry.Harness, len(processes))

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

	return harnessByPID
}

func pruneNestedHarnesses(
	processes []processinfo.Process,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) {
	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok || !catalog.IsWrapper(process) {
			continue
		}

		if launchesHarnessThroughWrappers(
			process.PID,
			harnessID,
			processes,
			tree,
			harnessByPID,
		) {
			delete(harnessByPID, process.PID)
		}
	}

	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok {
			continue
		}

		if hasAncestorHarness(process.PID, harnessID, tree, harnessByPID) {
			delete(harnessByPID, process.PID)
		}
	}
}

func processSightings(
	inventory cycleInventory,
	catalogByPID map[int]CatalogEntry,
	at time.Time,
) ([]registry.Observation, map[processKey]trackedProcess) {
	observations := make([]registry.Observation, 0, len(inventory.harnessByPID))
	current := make(map[processKey]trackedProcess)

	for _, process := range inventory.processes {
		harnessID, ok := inventory.harnessByPID[process.PID]
		if !ok {
			continue
		}

		key := processKey{harness: harnessID, pid: process.PID, start: process.StartIdentity}
		current[key] = trackedProcess{process: process, missingSince: time.Time{}, missingCount: 0}

		var identity registry.ObservationIdentity

		if entry, ok := catalogByPID[process.PID]; ok && entry.Harness == harnessID {
			identity = registry.ObservationIdentity{
				SessionID:   entry.SessionID,
				SessionPath: entry.SessionPath,
				CWD:         entry.CWD,
				Attributes:  nil,
			}
		}

		observations = append(
			observations,
			registry.Observation{
				Harness:  harnessID,
				At:       at,
				Subject:  identity,
				Evidence: &registry.Sighting{Process: *processIdentity(process), Present: true},
			},
		)
	}

	return observations, current
}

func paneLocation(pane mux.Pane) registry.Location {
	location := pane.Location
	if location.PanePID == 0 && len(pane.Processes) > 0 {
		location.PanePID = pane.Processes[0].PID
	}

	if location.PaneTTY == "" {
		location.PaneTTY = pane.ProcessTTY
	}

	return location
}

func catalogObservations(
	entries []CatalogEntry,
	processByPID map[int]processinfo.Process,
	at time.Time,
) []registry.Observation {
	observations := make([]registry.Observation, 0, len(entries))

	for _, entry := range entries {
		if entry.Harness == "" || entry.SessionID == "" {
			continue
		}

		identity := registry.ObservationIdentity{
			SessionID: entry.SessionID, SessionPath: entry.SessionPath, CWD: entry.CWD,
			Attributes: cloneAttributes(entry.Attributes),
		}
		metadata := &registry.Listing{
			ResumeCommand: append([]string(nil), entry.ResumeCommand...),
			CWD:           entry.CWD,
			ProjectRoot:   entry.ProjectRoot,
			ProcessPID:    entry.ProcessPID,
			Current:       entry.Current,
		}

		if entry.Current {
			if process, ok := catalogListedProcess(entry, processByPID); ok {
				metadata.ProcessStartIdentity = process.StartIdentity
			}
		}

		observations = append(
			observations,
			registry.Observation{
				Harness:  entry.Harness,
				At:       at,
				Subject:  identity,
				Evidence: metadata,
			},
		)
		if report, ok := catalogActivityReport(entry, processByPID); ok {
			observations = append(observations, catalog.PrepareObservation(registry.Observation{
				Harness: entry.Harness, At: at, Subject: identity, Evidence: report,
			}))
		}
	}

	return observations
}

func goneVerificationCount(observations []registry.Observation) int {
	count := 0

	for _, observation := range observations {
		if verification, ok := observation.Evidence.(*registry.Verification); !ok ||
			verification.Presence == registry.PresenceGone {
			count++
		}
	}

	return count
}

// holdUnconfirmedProcesses removes processes matched only by command name from
// this cycle until they are confirmed, so short-lived commands that share a
// harness executable name never become registry sessions.
func (o *Observer) holdUnconfirmedProcesses(
	sessions []registry.Session,
	catalogByPID map[int]CatalogEntry,
	processByPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
	at time.Time,
) map[processKey]time.Time {
	pending := make(map[processKey]time.Time)

	for pid, harnessID := range harnessByPID {
		key := processKey{harness: harnessID, pid: pid, start: processByPID[pid].StartIdentity}
		if o.processConfirmed(key, sessions, catalogByPID, at) {
			continue
		}

		firstSeen, ok := o.candidates[key]
		if !ok {
			firstSeen = at
		}

		pending[key] = firstSeen

		delete(harnessByPID, pid)
	}

	return pending
}

// processConfirmed reports whether a harness process may create or update a
// registry session. A process is confirmed by an existing session for the same
// process (native reports create one), by a current catalog entry, by earlier
// tracking, or by running for the confirmation period.
func (o *Observer) processConfirmed(
	key processKey,
	sessions []registry.Session,
	catalogByPID map[int]CatalogEntry,
	at time.Time,
) bool {
	if !o.continuous || o.confirmAfter < 0 {
		return true
	}

	if _, ok := o.tracked[key]; ok {
		return true
	}

	if entry, ok := catalogByPID[key.pid]; ok && entry.Harness == key.harness {
		return true
	}

	for _, session := range sessions {
		if session.Harness == key.harness && session.Process != nil &&
			session.Process.PID == key.pid &&
			session.Process.StartIdentity == key.start {
			return true
		}
	}

	firstSeen, ok := o.candidates[key]

	return ok && at.Sub(firstSeen) >= o.confirmAfter
}

func resolveHarness(process processinfo.Process) (registry.Harness, bool) {
	harnessID, ok := catalog.ResolveProcess(process)
	if !ok {
		return "", false
	}

	return observableHarness(process, harnessID)
}

func observableHarness(
	process processinfo.Process,
	harnessID registry.Harness,
) (registry.Harness, bool) {
	if process.Env[processinfo.ChildRoleEnv] != "" || process.PID == os.Getpid() ||
		isReportCommand(process.Args, harnessID) {
		return "", false
	}

	if filter, ok := catalog.ProcessFilterFor(harnessID); ok {
		if !filter.ObservableProcess(process) {
			return "", false
		}
	}

	return harnessID, true
}

func isReportCommand(args []string, harnessID registry.Harness) bool {
	for i, arg := range args {
		if arg == "report" && slices.Contains(args[i+1:], string(harnessID)) {
			return true
		}
	}

	return false
}

func hasAncestorHarness(
	pid int,
	harnessID registry.Harness,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) bool {
	for parent := range tree.parents(pid) {
		if harnessByPID[parent] == harnessID {
			return true
		}
	}

	return false
}

func observationsForUnlocatedProcesses(
	manifestLoader agentstate.Loader,
	sessions []registry.Session,
	processByPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
	locationPIDs map[int]bool,
	at time.Time,
	inspectScreen bool,
) []registry.Observation {
	observations := make([]registry.Observation, 0, len(harnessByPID))
	for pid, harnessID := range harnessByPID {
		if locationPIDs[pid] {
			continue
		}
		// harnessByPID is a filtered subset of processByPID from the same cycle snapshot.
		process := processByPID[pid]

		var emptyLocation registry.Location

		observations = append(
			observations,
			processObservation(harnessID, at, &registry.Placement{
				Process:  *processIdentity(process),
				Location: emptyLocation,
			}),
		)

		if !inspectScreen {
			continue
		}

		if screenObservation, detected := unavailableScreenState(
			manifestLoader,
			sessions,
			harnessID,
			process,
			at,
			"screen_not_in_supported_multiplexer",
		); detected {
			observations = append(observations, screenObservation)
		}
	}

	return observations
}

func unobservedSessionAbsence(
	session registry.Session,
	processByPID map[int]processinfo.Process,
	at time.Time,
) (registry.Observation, bool) {
	var empty registry.Observation

	if session.Process != nil && session.Process.Complete() {
		if process, ok := processByPID[session.Process.PID]; ok &&
			(process.StartIdentity == "" || process.StartIdentity == session.Process.StartIdentity) {
			return empty, false
		}

		present := false

		return processObservation(
			session.Harness,
			at,
			&registry.Sighting{Process: registry.ProcessIdentity{
				PPID: 0, ProcessGroupID: 0, Foreground: false, Executable: "", CWD: "", TTY: "",
				PID:           session.Process.PID,
				StartIdentity: session.Process.StartIdentity,
			}, Present: present},
		), true
	}

	panePID := session.Location.PanePID
	if session.Process == nil && panePID > 0 {
		if _, ok := processByPID[panePID]; ok {
			return empty, false
		}

		present := false

		identity := registry.ObservationIdentity{
			SessionID: session.SessionID, SessionPath: session.SessionPath, CWD: session.CWD,
			Attributes: nil,
		}
		if native := session.Observations.Native; native != nil {
			identity.Attributes = native.Attributes
		}

		return registry.Observation{
			Harness: session.Harness,
			At:      at,
			Subject: identity,
			Evidence: &registry.Sighting{
				Process: registry.ProcessIdentity{
					PID:            0,
					PPID:           0,
					ProcessGroupID: 0,
					Foreground:     false,
					StartIdentity:  "",
					Executable:     "",
					CWD:            "",
					TTY:            "",
				},
				Present: present,
			},
		}, true
	}

	return empty, false
}

func sessionForProcess(
	sessions []registry.Session,
	harnessID registry.Harness,
	identity *registry.ProcessIdentity,
) registry.Session {
	var session registry.Session

	session.Harness = harnessID
	session.Process = identity

	for _, candidate := range sessions {
		if candidate.Harness == harnessID && candidate.Process != nil &&
			candidate.Process.Equal(*identity) {
			return candidate
		}
	}

	return session
}

func screenFallbackMetadata(
	session registry.Session,
	harnessID registry.Harness,
	at time.Time,
) (string, string) {
	policy := (catalog.Rules{}).Policy(harnessID)
	if policy.Authority != registry.AuthorityHook {
		return "", ""
	}

	return policy.Reporter, registry.EvaluateHook(session, policy, at).Reason
}

func unavailableScreenState(
	manifestLoader agentstate.Loader,
	sessions []registry.Session,
	harnessID registry.Harness,
	process processinfo.Process,
	at time.Time,
	reason string,
) (registry.Observation, bool) {
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

	observation := screenObservation(session, harnessID, identity, at, at, agentstate.Decision{
		Activity:        registry.ActivityUnknown,
		Detail:          "",
		Reason:          reason,
		RuleID:          "",
		ManifestSource:  "",
		ManifestVersion: 0,
		Warning:         "",
		Evidence:        nil,
	})

	return observation, true
}

func (o *Observer) detectScreenState(
	ctx context.Context,
	sessions []registry.Session,
	harnessID registry.Harness,
	process processinfo.Process,
	pane mux.Pane,
	at time.Time,
) (registry.Observation, bool, error) {
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
		reason := pane.StateReason
		if reason == "" {
			reason = "multiplexer_agent_status"
		}

		observation := screenObservation(session, harnessID, identity, at, at, agentstate.Decision{
			Activity:        *pane.Activity,
			Detail:          "",
			Reason:          reason,
			RuleID:          "",
			ManifestSource:  "",
			ManifestVersion: 0,
			Warning:         "",
			Evidence:        nil,
		})

		return observation, true, nil
	}

	if o.disableScreenInspection {
		var empty registry.Observation
		return empty, false, nil
	}

	return o.captureScreenState(ctx, session, harnessID, identity, pane, at)
}

func (o *Observer) screenDecisionReady(
	sessions []registry.Session,
	harnessID registry.Harness,
	process processinfo.Process,
	observation registry.Observation,
) bool {
	if !o.continuous ||
		observation.Reading() == nil ||
		observation.ActivityClaim() == nil ||
		observation.Reading().Authority != registry.AuthorityScreen {
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
	if pending.activity != *observation.ActivityClaim() ||
		pending.ruleID != observation.Reading().RuleID {
		o.screenPending[key] = pendingScreenDecision{
			activity:      *observation.ActivityClaim(),
			ruleID:        observation.Reading().RuleID,
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

func (o *Observer) prunePendingScreenDecisions(current map[processKey]trackedProcess) {
	for key := range o.screenPending {
		if _, ok := current[key]; !ok {
			delete(o.screenPending, key)
		}
	}
}

func shouldDetectScreen(session registry.Session, at time.Time) bool {
	if catalog.SupportsScreen(session.Harness) {
		authority, _ := registry.ActivityAuthority(
			session,
			(catalog.Rules{}).Policy(session.Harness),
			at,
		)

		return authority == registry.AuthorityScreen
	}

	policy := (catalog.Rules{}).Policy(session.Harness)

	return policy.Authority == registry.AuthorityHook &&
		!registry.EvaluateHook(session, policy, at).Active
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
	if catalog.IsWrapper(process) {
		return false
	}

	_, ok := catalog.ResolveProcess(process)

	return ok
}

func catalogListedProcess(
	entry CatalogEntry,
	processes map[int]processinfo.Process,
) (processinfo.Process, bool) {
	process, ok := processes[entry.ProcessPID]
	if !ok || process.PID <= 0 || process.StartIdentity == "" {
		return process, false
	}

	if executableHarness, known := catalog.FromCommand(
		process.Executable,
	); known &&
		executableHarness != entry.Harness {
		return process, false
	}

	harnessID, ok := resolveHarness(process)

	return process, ok && harnessID == entry.Harness
}

func catalogActivityReport(
	entry CatalogEntry,
	processes map[int]processinfo.Process,
) (*registry.Report, bool) {
	if !entry.Current || entry.Reporter.Integration == "" || !entry.Activity.Valid() {
		return nil, false
	}

	process, ok := catalogListedProcess(entry, processes)
	if !ok {
		return nil, false
	}

	return &registry.Report{
		DetailObservedAt: nil,
		Detail:           nil,
		Reporter:         entry.Reporter,
		Event:            "catalog.activity",
		Lifecycle:        nil,
		Claim:            nil,
		Activity:         new(entry.Activity),
		Process:          processIdentity(process),
		Location:         nil,
		Attributes:       cloneAttributes(entry.Attributes),
		Payload:          nil,
		Listing: &registry.Listing{
			ResumeCommand: append([]string(nil), entry.ResumeCommand...), CWD: entry.CWD,
			ProjectRoot: entry.ProjectRoot, ProcessPID: entry.ProcessPID, Current: entry.Current,
		},
	}, true
}

func processIdentity(process processinfo.Process) *registry.ProcessIdentity {
	return &registry.ProcessIdentity{
		PID:            process.PID,
		PPID:           process.PPID,
		ProcessGroupID: process.ProcessGroupID,
		Foreground:     process.Foreground,
		StartIdentity:  process.StartIdentity,
		Executable:     process.Executable,
		CWD:            process.CWD,
		TTY:            process.TTY,
	}
}

func (o *Observer) initializeTracked(ctx context.Context) error {
	if o.initialized {
		return nil
	}

	sessions, err := o.store.List(ctx, registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		return fmt.Errorf("listing live sessions: %w", err)
	}

	for _, session := range sessions {
		observation := session.Observations.Process
		if observation == nil || !observation.Sighting.Present ||
			!observation.Sighting.Process.Complete() {
			continue
		}

		process := processinfo.Process{
			PID:            observation.Sighting.Process.PID,
			Name:           "",
			PPID:           observation.Sighting.Process.PPID,
			ProcessGroupID: observation.Sighting.Process.ProcessGroupID,
			Foreground:     observation.Sighting.Process.Foreground,
			StartIdentity:  observation.Sighting.Process.StartIdentity,
			Executable:     observation.Sighting.Process.Executable,
			CWD:            observation.Sighting.Process.CWD,
			TTY:            observation.Sighting.Process.TTY,
			Env:            nil,
			Args:           nil,
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

func (o *Observer) releaseLock() error {
	o.mu.Lock()
	file := o.lockFile
	o.lockFile = nil
	o.running = false
	o.mu.Unlock()

	return closeObserverLock(file)
}

func (o *Observer) recordHealth(
	at time.Time,
	degraded bool,
	category string,
	err error,
	result Result,
) error {
	health, shouldWrite := o.updateHealth(at, degraded, category, err, result)
	if !shouldWrite || o.healthPath == "" {
		return nil
	}

	if err := o.writeHealth(health); err != nil {
		return err
	}

	o.mu.Lock()
	o.lastHealthWrite = at
	o.mu.Unlock()

	return nil
}

func (o *Observer) updateHealth(
	at time.Time,
	degraded bool,
	category string,
	err error,
	result Result,
) (Health, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	wasDegraded := o.health.Degraded

	previousCategory := o.health.LastEnumerationErrorCategory
	if o.startedAt.IsZero() {
		o.startedAt = at
		o.health.PID = os.Getpid()
		o.health.StartedAt = at
		o.health.Interval = o.interval
		o.health.GracePeriod = o.grace
	}

	o.health.LastAttemptAt = at
	o.health.Cycles++
	o.health.Observations += int64(result.Observations)
	o.health.Sessions += int64(result.Sessions)

	o.health.Degraded = degraded
	if err != nil {
		o.health.LastEnumerationErrorCategory = category
		o.health.LastEnumerationError = err.Error()
	} else if !degraded {
		o.health.LastSuccessAt = at
		o.health.LastEnumerationErrorCategory = ""
		o.health.LastEnumerationError = ""
	}

	// Rewrite only on a status or error-category change, or on the slow
	// interval, so steady and persistently degraded cycles do not write.
	shouldWrite := o.lastHealthWrite.IsZero() || at.Sub(o.lastHealthWrite) >= healthWriteInterval ||
		degraded != wasDegraded || o.health.LastEnumerationErrorCategory != previousCategory

	return o.health, shouldWrite
}

func (o *Observer) writeHealth(health Health) error {
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

	return nil
}

func (o *Observer) isProcessTracked(session registry.Session) bool {
	if o == nil || session.Process == nil {
		return false
	}

	for key := range o.tracked {
		if key.harness == session.Harness && key.pid == session.Process.PID {
			if key.start == "" || session.Process.StartIdentity == "" ||
				key.start == session.Process.StartIdentity {
				return true
			}
		}
	}

	return false
}

// reconcileUnobservedSessions retires sessions whose recorded owners no longer
// exist and are not covered by the tracked-process sweep. Sessions without a
// verifiable process or location become unknown while retaining native evidence.
func (o *Observer) reconcileUnobservedSessions(
	ctx context.Context,
	sessions []registry.Session,
	processByPID map[int]processinfo.Process,
	at time.Time,
) ([]registry.Observation, error) {
	observations := make([]registry.Observation, 0)

	var checkErrors []error

	for _, session := range sessions {
		if session.Presence() == registry.PresenceGone {
			continue
		}

		if o.isProcessTracked(session) {
			continue
		}

		if observation, ok := unobservedSessionAbsence(session, processByPID, at); ok {
			observations = append(observations, observation)
			continue
		}

		if session.Process != nil && session.Process.Complete() {
			continue
		}

		presence, err := o.locationPresence(ctx, session.Location)
		if err != nil {
			checkErrors = append(
				checkErrors,
				fmt.Errorf("check session %s location: %w", session.ID, err),
			)
			presence = registry.PresenceUnknown
		}

		reason := "owner_unverified"
		if presence == registry.PresenceGone {
			reason = "location_gone"
		} else {
			presence = registry.PresenceUnknown
		}

		if session.Presence() == presence && session.Decision() != nil &&
			session.Decision().Reason == reason {
			continue
		}

		observations = append(observations, registry.Observation{
			Harness: session.Harness,
			At:      at,
			Subject: registry.ObservationIdentity{
				SessionID:   session.SessionID,
				SessionPath: session.SessionPath,
				CWD:         "",
				Attributes:  nil,
			},
			Evidence: &registry.Verification{Presence: presence, Reason: reason},
		})
	}

	return observations, errors.Join(checkErrors...)
}

func (o *Observer) captureScreenState(
	ctx context.Context,
	session registry.Session,
	harnessID registry.Harness,
	identity *registry.ProcessIdentity,
	pane mux.Pane,
	at time.Time,
) (registry.Observation, bool, error) {
	manifest, err := o.manifestLoader.Load(harnessID)
	if err != nil {
		return registry.Observation{}, false, fmt.Errorf(
			"loading %s detection manifest: %w",
			harnessID,
			err,
		)
	}

	snapshot, captureErr := o.screenCapture(ctx, pane)
	if captureErr != nil {
		return registry.Observation{}, false, fmt.Errorf(
			"capturing %s pane %s for detection: %w",
			harnessID,
			pane.Location.PaneID,
			captureErr,
		)
	}

	decision := manifest.Evaluate(agentstate.NormalizeSnapshot(snapshot.Text, snapshot.Title))
	if decision.Activity == registry.ActivityUnknown &&
		decision.Reason == agentstate.ReasonNoRuleMatched &&
		session.Activity() != nil &&
		*session.Activity() != registry.ActivityUnknown {
		var empty registry.Observation
		return empty, false, nil
	}

	observedAt := screenObservationTime(at, o.now().UTC())
	observation := screenObservation(session, harnessID, identity, at, observedAt, decision)

	if manifest.Warning != "" {
		return observation, true, fmt.Errorf(
			"%w: %s",
			errDetectionOverrideInvalid,
			manifest.Warning,
		)
	}

	return observation, true, nil
}

func (o *Observer) observeBatch(
	ctx context.Context,
	observations []registry.Observation,
) ([]registry.Session, error) {
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
			observationErrs = append(
				observationErrs,
				fmt.Errorf("observation %d: %w", index, observeErr),
			)

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
		r.Observations,
		r.Sessions,
		r.Processes,
		r.Panes,
		r.Catalog,
		r.Present,
		r.Gone,
		r.Changed,
		r.Degraded,
	)
	if r.Error != "" {
		result += " error=" + strconv.Quote(r.Error)
	}

	return result
}

func (r Result) withDegradation(err error) Result {
	r.Degraded = true
	r.Error = err.Error()

	return r
}

func panePlacementObservations(
	inventory cycleInventory, pane mux.Pane, process processinfo.Process, selected bool,
	locationPIDs map[int]bool, at time.Time,
) []registry.Observation {
	var observations []registry.Observation

	for _, candidate := range inventory.processes {
		candidateHarness := inventory.harnessByPID[candidate.PID]
		if candidateHarness == "" || locationPIDs[candidate.PID] ||
			((!selected || candidate.PID != process.PID) &&
				!processTiedToPane(candidate, pane, inventory.tree)) {
			continue
		}

		observations = append(
			observations,
			processObservation(candidateHarness, at, &registry.Placement{
				Process:  *processIdentity(candidate),
				Location: paneLocation(pane),
			}),
		)
		locationPIDs[candidate.PID] = true
	}

	return observations
}

func screenObservation(
	session registry.Session,
	harnessID registry.Harness,
	identity *registry.ProcessIdentity,
	at time.Time,
	observedAt time.Time,
	decision agentstate.Decision,
) registry.Observation {
	fallback, fallbackReason := screenFallbackMetadata(session, harnessID, at)

	var subject registry.ObservationIdentity

	return registry.Observation{
		Harness: harnessID,
		At:      observedAt,
		Subject: subject,
		Evidence: (*registry.Reading)(&registry.ScreenObservation{
			Activity:               decision.Activity,
			Detail:                 decision.Detail,
			Authority:              registry.AuthorityScreen,
			Reason:                 decision.Reason,
			RuleID:                 decision.RuleID,
			ManifestSource:         decision.ManifestSource,
			ManifestVersion:        decision.ManifestVersion,
			FallbackForIntegration: fallback,
			FallbackReason:         fallbackReason,
			Process:                *identity,
			ObservedAt:             observedAt,
		}),
	}
}

func processObservation(
	harnessID registry.Harness,
	at time.Time,
	evidence registry.Evidence,
) registry.Observation {
	var subject registry.ObservationIdentity
	return registry.Observation{Harness: harnessID, At: at, Subject: subject, Evidence: evidence}
}
