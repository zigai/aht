package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"

	"github.com/zigai/aht/v2/internal/processinfo"

	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

//exhaustruct:optional
type Options struct {
	DetailObservedAt string
	Detail           string
	Reporter         string
	ReporterVersion  int
	MultiSession     bool
	Harness          string
	Presence         string
	Activity         string
	Lifecycle        string
	SessionID        string
	SessionPath      string
	CWD              string
	CWDAuto          bool
	ProjectRoot      string
	PID              int
	PPID             int
	ProcessGroupID   int
	StartIdentity    string
	Executable       string
	TTY              string
	Event            string
	ObservedAt       string
	Sequence         string
	Attributes       []string
	RawStdin         bool
	RawDefaultsOnly  bool
	NoTmux           bool
	ResumeCommand    []string
	Evidence         string
}

type Result struct {
	Harness registry.Harness
	//exhaustruct:optional
	Observation registry.Observation
	//exhaustruct:optional
	Ignored bool
}

type reportState struct {
	presence   registry.Presence
	activity   registry.Activity
	lifecycle  registry.NativeLifecycle
	observedAt time.Time
}

type Runtime struct {
	Location  registry.Location
	Processes []processinfo.Process

	//exhaustruct:optional
	DefaultObservedAt time.Time
}

func ApplyHarnessEnv(opts *Options) {
	harnessID, err := catalog.Parse(opts.Harness)
	if err != nil {
		return
	}

	if opts.SessionID == "" {
		opts.SessionID = firstEnv(catalog.EnvNames(harnessID, harness.EnvSessionID)...)
	}

	if opts.SessionPath == "" {
		opts.SessionPath = firstEnv(catalog.EnvNames(harnessID, harness.EnvSessionPath)...)
	}

	if opts.PID <= 0 {
		opts.PID = firstEnvInt(catalog.EnvNames(harnessID, harness.EnvPID)...)
	}

	if opts.Event == "" {
		opts.Event = firstEnv(catalog.EnvNames(harnessID, harness.EnvEvent)...)
	}
}

func CollectRuntime(ctx context.Context, pid int, noTmux bool) Runtime {
	return Runtime{Location: collectLocation(ctx, noTmux), Processes: processAncestors(ctx, pid)}
}

func Prepare(
	stdin io.Reader,
	opts Options,
	runtime Runtime,
) (Result, error) {
	harnessID, attrs, err := parseReportTarget(opts)
	if err != nil {
		return Result{}, err
	}

	rawPayload, defaultsPayload, err := readStdinPayloadData(
		stdin, harnessID,
		opts.RawStdin,
		opts.RawDefaultsOnly,
	)
	if err != nil {
		return Result{}, err
	}

	if !catalog.PayloadCompatible(harnessID, defaultsPayload) {
		return Result{Harness: harnessID, Ignored: true}, nil
	}

	defaults, err := catalog.PayloadDefaults(harnessID, defaultsPayload)
	if err != nil {
		return Result{}, fmt.Errorf("derive payload defaults: %w", err)
	}

	opts.ProjectRoot = resolveReportProjectRoot(harnessID, opts.ProjectRoot, defaults, opts.CWD)
	applyPayloadDefaults(&opts, attrs, defaults)
	applyReportRuntimeDefaults(&opts)
	translateReportLifecycle(&opts, harnessID, attrs)

	state, err := normalizeReportState(opts, runtime.DefaultObservedAt)
	if err != nil {
		return Result{}, err
	}

	state.activity, err = catalog.ActivityFromPayload(
		harnessID,
		opts.Event,
		state.activity,
		defaultsPayload,
		state.observedAt,
	)
	if err != nil {
		return Result{}, fmt.Errorf("derive payload activity: %w", err)
	}

	observation, err := buildReportObservation(harnessID, opts, runtime, attrs, rawPayload, state)
	if err != nil {
		return Result{}, err
	}

	observation = catalog.PrepareObservation(observation)
	if err := applyReportDetail(&observation, opts, state.activity, state.observedAt); err != nil {
		return Result{}, err
	}

	return Result{Harness: harnessID, Observation: observation}, nil
}

func parseReportSequence(value string) (uint64, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}

	sequence, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parsing sequence: %w", err)
	}

	return sequence, true, nil
}

func parseReportTarget(opts Options) (registry.Harness, map[string]string, error) {
	if opts.RawStdin && opts.RawDefaultsOnly {
		return "", nil, invalidInput(errConflictingReportStdin)
	}

	if strings.TrimSpace(opts.Harness) == "" {
		return "", nil, invalidInput(errMissingReportHarness)
	}

	harnessID, err := catalog.Parse(opts.Harness)
	if err != nil {
		return "", nil, invalidInput(fmt.Errorf("normalizing harness: %w", err))
	}

	attrs, err := parseAttributes(opts.Attributes)
	if err != nil {
		return "", nil, invalidInput(err)
	}

	return harnessID, attrs, nil
}

func translateReportLifecycle(
	opts *Options,
	harnessID registry.Harness,
	attrs map[string]string,
) {
	if strings.EqualFold(opts.Evidence, "process") {
		return
	}

	translated := catalog.LifecycleFor(harnessID, opts.Event, attrs)

	opts.Event = translated.Event
	if opts.Lifecycle == "" {
		opts.Lifecycle = string(translated.Lifecycle)
	}

	if opts.Presence == "" {
		opts.Presence = string(translated.Presence)
	}
}

func normalizeReportState(opts Options, defaultObservedAt time.Time) (reportState, error) {
	presence, err := registry.NormalizePresence(opts.Presence)
	if err != nil {
		return reportState{}, invalidInput(fmt.Errorf("normalize presence: %w", err))
	}

	activity, err := registry.NormalizeActivity(opts.Activity)
	if err != nil {
		return reportState{}, invalidInput(fmt.Errorf("normalize activity: %w", err))
	}

	if presence == registry.PresenceGone && activity != "" {
		return reportState{}, invalidInput(errGonePresenceActivity)
	}

	lifecycle, err := normalizeReportLifecycle(opts.Lifecycle)
	if err != nil {
		return reportState{}, invalidInput(err)
	}

	if lifecycle == registry.NativeLifecycleEnd && activity != "" {
		return reportState{}, invalidInput(errGonePresenceActivity)
	}

	observedAt, err := parseObservedAt(opts.ObservedAt)
	if err != nil {
		return reportState{}, invalidInput(err)
	}

	if observedAt.IsZero() {
		observedAt = defaultObservedAt
	}

	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return reportState{
		presence:   presence,
		activity:   activity,
		lifecycle:  lifecycle,
		observedAt: observedAt,
	}, nil
}

func buildReportObservation(
	harnessID registry.Harness,
	opts Options,
	runtime Runtime,
	attrs map[string]string,
	rawPayload json.RawMessage,
	state reportState,
) (registry.Observation, error) {
	sequence, sequenceSet, err := parseReportSequence(opts.Sequence)
	if err != nil {
		return registry.Observation{}, invalidInput(err)
	}

	if !reportHasIdentity(opts, state) {
		return registry.Observation{}, invalidInput(errMissingReportIdentity)
	}

	identity := registry.ObservationIdentity{
		CWD:         "",
		Attributes:  nil,
		SessionID:   opts.SessionID,
		SessionPath: opts.SessionPath,
	}

	var observation registry.Observation

	if strings.EqualFold(opts.Evidence, "process") {
		observation, err = processReportObservation(
			harnessID,
			identity,
			opts,
			runtime.Processes,
			state.presence,
			state.activity,
			state.observedAt,
			sequenceSet,
		)
		if err != nil {
			return registry.Observation{}, err
		}
	} else {
		observation = nativeReportObservation(
			harnessID,
			identity,
			opts,
			runtime,
			attrs,
			rawPayload,
			state.presence,
			state.activity,
			state.lifecycle,
			state.observedAt,
		)
		if sequenceSet {
			observation.Report().Reporter.Sequence = &sequence
		}
	}

	if opts.CWD != "" || opts.ProjectRoot != "" || len(opts.ResumeCommand) > 0 {
		observation.SetListing(
			&registry.Listing{
				ResumeCommand: append([]string(nil), opts.ResumeCommand...),
				CWD:           opts.CWD,
				ProjectRoot:   opts.ProjectRoot,
			},
		)
	}

	return observation, nil
}

func reportHasIdentity(opts Options, state reportState) bool {
	return state.presence != "" || state.activity != "" || state.lifecycle != "" ||
		opts.Event != "" ||
		opts.SessionID != "" ||
		opts.SessionPath != ""
}

func processReportObservation(
	harnessID registry.Harness,
	identity registry.ObservationIdentity,
	opts Options,
	processes []processinfo.Process,
	presence registry.Presence,
	activity registry.Activity,
	observedAt time.Time,
	sequenceSet bool,
) (registry.Observation, error) {
	if opts.PID <= 0 {
		return registry.Observation{}, invalidInput(errProcessEvidenceIdentity)
	}

	if activity != "" {
		return registry.Observation{}, invalidInput(errProcessEvidenceActivity)
	}

	if sequenceSet {
		return registry.Observation{}, invalidInput(errProcessEvidenceSequence)
	}

	process := processEvidenceIdentity(opts, processes)
	if process == nil || !process.Complete() {
		return registry.Observation{}, invalidInput(errProcessEvidenceIdentity)
	}

	present := presence != registry.PresenceGone

	return registry.Observation{
		Harness:  harnessID,
		At:       observedAt,
		Subject:  identity,
		Evidence: &registry.Sighting{Process: *process, Present: present},
	}, nil
}

func applyReportDetail(
	observation *registry.Observation,
	opts Options,
	activity registry.Activity,
	observedAt time.Time,
) error {
	if opts.Detail != "" {
		if observation.Report() == nil {
			return invalidInput(
				fmt.Errorf("%w: detail requires a native report", registry.ErrInvalidObservation),
			)
		}

		detail := registry.ActivityDetail(opts.Detail)
		if opts.Detail == "clear" {
			detail = ""
		}

		if detail != "" && !detail.ValidFor(activity) {
			return invalidInput(
				fmt.Errorf("%w: detail does not match activity", registry.ErrInvalidObservation),
			)
		}

		observation.Report().Detail = &detail
	}

	return applyReportDetailTimestamp(observation, opts.DetailObservedAt, observedAt)
}

func applyReportDetailTimestamp(
	observation *registry.Observation,
	timestamp string,
	observedAt time.Time,
) error {
	if timestamp != "" {
		detailAt, err := parseObservedAt(timestamp)
		if err != nil || detailAt.IsZero() || detailAt.After(observedAt) {
			return invalidInput(
				fmt.Errorf("%w: invalid detail timestamp", registry.ErrInvalidObservation),
			)
		}

		if observation.Report() == nil || observation.Report().Detail == nil ||
			*observation.Report().Detail == "" {
			return invalidInput(
				fmt.Errorf(
					"%w: detail timestamp requires specific detail",
					registry.ErrInvalidObservation,
				),
			)
		}

		observation.Report().DetailObservedAt = &detailAt
	}

	return nil
}

func nativeReportObservation(
	harnessID registry.Harness,
	identity registry.ObservationIdentity,
	opts Options,
	runtime Runtime,
	attributes map[string]string,
	rawPayload json.RawMessage,
	presence registry.Presence,
	activity registry.Activity,
	lifecycle registry.NativeLifecycle,
	observedAt time.Time,
) registry.Observation {
	observation := registry.Observation{
		Harness: harnessID,
		At:      observedAt,
		Subject: identity,
		Evidence: &registry.Report{
			Lifecycle: nil,
			Claim:     nil,
			Activity:  nil,
			Location:  nil,
			Listing:   nil,
			Reporter: registry.Reporter{
				Sequence:     nil,
				Integration:  opts.Reporter,
				Version:      opts.ReporterVersion,
				MultiSession: opts.MultiSession,
			},
			Event:      opts.Event,
			Process:    processIdentity(harnessID, runtime.Processes),
			Attributes: attributes,
			Payload:    rawPayload,
			Detail:     nil,
		},
	}
	if lifecycle != "" {
		observation.Report().Lifecycle = &lifecycle
	}

	if location := runtime.reportLocation(harnessID); !location.Empty() {
		observation.SetLocation(&location)
	}

	if presence != "" {
		observation.Report().Claim = &presence
	}

	if activity != "" {
		observation.SetActivity(&activity)
	}

	if observation.Report().Event == "" && (presence != "" || activity != "" || lifecycle != "") {
		observation.Report().Event = "cli"
	}

	return observation
}

func normalizeReportLifecycle(value string) (registry.NativeLifecycle, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}

	lifecycle, err := registry.NormalizeLifecycle(value)
	if err != nil {
		return "", fmt.Errorf("normalize lifecycle: %w", err)
	}

	return lifecycle, nil
}

func processEvidenceIdentity(
	opts Options,
	processes []processinfo.Process,
) *registry.ProcessIdentity {
	if opts.StartIdentity != "" {
		return &registry.ProcessIdentity{
			Foreground:     false,
			PID:            opts.PID,
			PPID:           opts.PPID,
			ProcessGroupID: opts.ProcessGroupID,
			StartIdentity:  opts.StartIdentity,
			Executable:     opts.Executable,
			CWD:            opts.CWD,
			TTY:            opts.TTY,
		}
	}

	for _, process := range processes {
		if process.PID != opts.PID {
			continue
		}

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

	return nil
}

func collectLocation(ctx context.Context, noTmux bool) registry.Location {
	for _, driver := range drivers.All() {
		if noTmux && driver.Kind() == registry.MultiplexerTmux {
			continue
		}

		location, err := driver.Current(ctx)
		if err == nil && !location.Empty() {
			return location
		}
	}

	var empty registry.Location

	return empty
}

func processAncestors(ctx context.Context, pid int) []processinfo.Process {
	if pid <= 0 {
		pid = os.Getppid()
	}

	var processes []processinfo.Process

	for range reportProcessAncestorLimit {
		process, found, err := processinfo.Find(ctx, pid)
		if err != nil || !found {
			break
		}

		processes = append(processes, process)
		if process.PPID <= 0 || process.PPID == process.PID {
			break
		}

		pid = process.PPID
	}

	return processes
}

func processIdentity(
	harnessID registry.Harness,
	processes []processinfo.Process,
) *registry.ProcessIdentity {
	index, ok := harnessProcessIndex(harnessID, processes)
	if !ok {
		return nil
	}

	selected := processes[index]

	return &registry.ProcessIdentity{
		PID:            selected.PID,
		PPID:           selected.PPID,
		ProcessGroupID: selected.ProcessGroupID,
		Foreground:     selected.Foreground,
		StartIdentity:  selected.StartIdentity,
		Executable:     selected.Executable,
		CWD:            selected.CWD,
		TTY:            selected.TTY,
	}
}

func harnessProcessIndex(harnessID registry.Harness, processes []processinfo.Process) (int, bool) {
	selected := -1

	for index, process := range processes {
		matches := reportProcessMatchesHarness(process, harnessID)
		if selected >= 0 && !matches {
			break
		}

		if matches && (selected < 0 || !catalog.IsWrapper(process)) {
			selected = index
		}
	}

	return selected, selected >= 0
}

func (r Runtime) reportLocation(harnessID registry.Harness) registry.Location {
	paneTTY := r.Location.PaneTTY

	index, ok := harnessProcessIndex(harnessID, r.Processes)
	if !ok || paneTTY == "" || r.Processes[index].TTY == "" ||
		r.Processes[index].TTY == paneTTY {
		return r.Location
	}

	for _, ancestor := range r.Processes[index+1:] {
		if _, isAgent := catalog.ResolveProcess(ancestor); isAgent && ancestor.TTY == paneTTY {
			// An agent in the pane started this one in a terminal of its own, for
			// example to test it. The multiplexer variables are inherited, and the
			// pane shows only the parent agent.
			var empty registry.Location

			return empty
		}
	}

	return r.Location
}

func reportProcessMatchesHarness(process processinfo.Process, expected registry.Harness) bool {
	harnessID, ok := catalog.ResolveProcess(process)

	return ok && harnessID == expected
}

func parentProcessArgs(ctx context.Context) []string {
	process, found, err := processinfo.Find(ctx, os.Getppid())
	if err != nil || !found {
		return nil
	}

	return process.Args
}

func applyPayloadDefaults(opts *Options, a map[string]string, d harness.PayloadDefaults) {
	if opts.SessionID == "" {
		opts.SessionID = d.SessionID
	}

	if opts.SessionPath == "" {
		opts.SessionPath = d.SessionPath
	}

	if opts.Event == "" {
		opts.Event = d.Event
	}

	applyCWDDefault(opts, d.CWD)
	maps.Copy(a, d.Attributes)
}

func applyReportRuntimeDefaults(opts *Options) {
	if opts.CWD == "" && opts.CWDAuto {
		if wd, err := os.Getwd(); err == nil {
			opts.CWD = wd
		}
	}
}

func applyCWDDefault(opts *Options, v string) {
	if v != "" && opts.CWDAuto && opts.CWD == "" {
		opts.CWD = v
	}
}

func resolveReportProjectRoot(
	harnessID registry.Harness,
	projectRoot string,
	payload harness.PayloadDefaults,
	cwd string,
) string {
	if projectRoot != "" {
		return projectRoot
	}

	if root := firstEnv(catalog.EnvNames(harnessID, harness.EnvProjectRoot)...); root != "" {
		return root
	}

	if payload.ProjectRoot != "" {
		return payload.ProjectRoot
	}

	if root := findProjectRoot(payload.CWD); root != "" {
		return root
	}

	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	return findProjectRoot(cwd)
}

func parseObservedAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing observed-at: %w", err)
	}

	return t, nil
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}

	return ""
}

func firstEnvInt(names ...string) int { v := firstEnv(names...); n, _ := strconv.Atoi(v); return n }

func findProjectRoot(start string) string {
	if start == "" {
		return ""
	}

	d, _ := filepath.Abs(start)
	for {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}

		p := filepath.Dir(d)
		if p == d {
			return ""
		}

		d = p
	}
}

func parseAttributes(values []string) (map[string]string, error) {
	a := map[string]string{}

	for _, v := range values {
		k, x, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%w: must be key=value: %q", errInvalidAttribute, v)
		}

		a[strings.TrimSpace(k)] = x
	}

	return a, nil
}

func readStdinPayloadData(
	stdin io.Reader, harnessID registry.Harness,
	storeRaw bool, defaultsOnly bool,
) (json.RawMessage, json.RawMessage, error) {
	if !storeRaw && !defaultsOnly {
		return nil, nil, nil
	}

	d, err := readReportPayloadInput(stdin, harnessID)
	if err != nil {
		return nil, nil, fmt.Errorf("read stdin payload: %w", err)
	}

	p, err := normalizeRawPayloadBytes(d)
	if err != nil {
		return nil, nil, fmt.Errorf("encode stdin payload: %w", err)
	}

	if storeRaw {
		return p, p, nil
	}

	return nil, p, nil
}
