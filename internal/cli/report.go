package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"

	"github.com/zigai/aht/v2/internal/processinfo"

	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
	"github.com/zigai/aht/v2/pkg/zellij"
)

type reportOptions struct {
	detailObservedAt string
	detail           string
	reporter         string
	reporterVersion  int
	multiSession     bool
	harness          string
	presence         string
	activity         string
	lifecycle        string
	sessionID        string
	sessionPath      string
	cwd              string
	cwdAuto          bool
	projectRoot      string
	projectRootAuto  bool
	pid              int
	ppid             int
	processGroupID   int
	startIdentity    string
	executable       string
	tty              string
	event            string
	observedAt       string
	sequence         string
	attributes       []string
	rawStdin         bool
	rawDefaultsOnly  bool
	noTmux           bool
	quiet            bool
	resumeCommand    []string
	evidence         string
}

type preparedReport struct {
	harness     registry.Harness
	observation registry.Observation
	ignored     bool
}

type reportRuntimeContext struct {
	tmux        registry.Location
	multiplexer registry.Location
	processes   []processinfo.Process

	defaultObservedAt time.Time
}

func (app *application) newReportCommand() *cobra.Command {
	options := defaultReportOptionsFromEnv()
	cmd := &cobra.Command{
		Use:           "report [harness]",
		Short:         "Record a harness observation",
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if options.harness != "" {
					return exitCode(fmt.Errorf("%w: harness already set", errUnexpectedReportArg), exitCodeUsage)
				}
				options.harness = args[0]
			}
			if cmd.Flags().Changed("cwd") {
				options.cwdAuto = false
			}
			if cmd.Flags().Changed("project-root") {
				options.projectRootAuto = false
			}
			stdin := app.stdin
			if stdin == nil {
				stdin = os.Stdin
			}
			return app.runReport(cmd.Context(), stdin, options)
		},
	}
	f := cmd.Flags()
	f.StringVar(&options.detailObservedAt, "detail-observed-at", "", "original detail evidence RFC3339 timestamp")
	f.StringVar(&options.detail, "detail", "", "activity detail: permission, question, general, usage_limit, or clear")
	f.StringVar(&options.reporter, "reporter", options.reporter, "reporting integration identity")
	f.IntVar(&options.reporterVersion, "reporter-version", options.reporterVersion, "reporting integration version")
	f.BoolVar(&options.multiSession, "multi-session", options.multiSession, "reporter supports multiple sessions per process")
	f.StringVar(&options.presence, "presence", options.presence, "presence: `<val>` (live, gone, unknown)")
	f.StringVar(&options.activity, "activity", options.activity, "reported activity hint: `<val>` (running, waiting, idle, unknown)")
	f.StringVar(&options.lifecycle, "lifecycle", options.lifecycle, "native lifecycle: `<val>` (start, resume, end)")
	_ = f.MarkHidden("lifecycle")
	f.StringVar(&options.sessionID, "session-id", options.sessionID, "harness session `<id>`")
	f.StringVar(&options.sessionPath, "session-path", options.sessionPath, "harness session file `<path>`")
	f.StringVar(&options.cwd, "cwd", options.cwd, "agent current working `<dir>`")
	f.StringVar(&options.projectRoot, "project-root", options.projectRoot, "project `<root>`")
	f.IntVar(&options.pid, "pid", options.pid, "agent process `<id>`")
	f.IntVar(&options.ppid, "ppid", options.ppid, "agent parent process `<id>`")
	f.IntVar(&options.processGroupID, "process-group-id", options.processGroupID, "agent process group `<id>`")
	f.StringVar(&options.startIdentity, "start-identity", options.startIdentity, "process start `<identity>`")
	f.StringVar(&options.executable, "executable", options.executable, "resolved executable `<path>`")
	f.StringVar(&options.tty, "tty", options.tty, "agent `<tty>`")
	f.StringVar(&options.event, "event", options.event, "native harness event `<name>`")
	f.StringVar(&options.observedAt, "observed-at", options.observedAt, "RFC3339 `<timestamp>`")
	f.StringVar(&options.sequence, "sequence", options.sequence, "strictly increasing integration report `<seq>`")
	f.StringArrayVar(&options.attributes, "attribute", nil, "extra `<key=value>` attribute")
	f.StringArrayVar(&options.resumeCommand, "resume-command", nil, "resume command argv `<item>`, repeatable")
	f.StringVar(&options.evidence, "evidence", options.evidence, "evidence `<kind>` (managed shims)")
	f.BoolVar(&options.rawStdin, "raw-stdin", false, "store stdin as raw hook payload")
	f.BoolVar(&options.rawDefaultsOnly, "raw-stdin-defaults-only", false, "read stdin for defaults without storing raw payload")
	f.BoolVar(&options.noTmux, "no-tmux", false, "do not collect tmux context")
	_ = f.MarkHidden("evidence")
	f.BoolVarP(&options.quiet, "quiet", "q", false, "suppress human-readable output")
	return cmd
}

func defaultReportOptionsFromEnv() reportOptions {
	return reportOptions{harness: firstEnv("AHT_HARNESS", "AGENT_HARNESS"), sessionID: firstEnv(catalog.EnvNames(harness.EnvSessionID)...), sessionPath: firstEnv(catalog.EnvNames(harness.EnvSessionPath)...), cwdAuto: true, projectRoot: firstEnv(catalog.EnvNames(harness.EnvProjectRoot)...), pid: firstEnvInt(catalog.EnvNames(harness.EnvPID)...), ppid: firstEnvInt("AHT_PPID", "AGENT_PPID"), tty: firstEnv("AHT_TTY", "TTY"), event: firstEnv(catalog.EnvNames(harness.EnvEvent)...), sequence: firstEnv("AHT_SEQUENCE")}
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

func (app *application) runReport(ctx context.Context, stdin io.Reader, opts reportOptions) error {
	prepared, err := prepareReport(stdin, opts, reportRuntimeContext{
		tmux:        reportTmuxLocation(ctx, opts.noTmux),
		multiplexer: reportMultiplexerLocation(),
		processes:   reportProcessAncestors(ctx, opts.pid),
	})
	if err != nil {
		return err
	}
	if prepared.ignored {
		return app.writeReportIgnored(prepared.harness, "payload_mismatch", "hook payload does not match harness", opts.quiet)
	}
	session, err := app.registryStore().Observe(ctx, prepared.observation)
	if errors.Is(err, registry.ErrProcessEnded) {
		// A late hook from an exited process is expected, not a hook failure.
		return app.writeReportIgnored(prepared.harness, "process_ended", "reporting process already ended", opts.quiet)
	}
	if err != nil {
		return fmt.Errorf("recording observation: %w", err)
	}
	return app.writeReportResult(session, opts.quiet)
}

func (app *application) writeReportIgnored(harnessID registry.Harness, reason, detail string, quiet bool) error {
	if app.outputJSON {
		return app.writeJSON(map[string]string{statusCommandName: "ignored", "harness": string(harnessID), "reason": reason})
	}
	if quiet {
		return nil
	}
	return app.writef("ignored %s report: %s\n", harnessID, detail)
}

//nolint:gocognit,cyclop,nestif // report preparation validates independent evidence dimensions in order
func prepareReport(stdin io.Reader, opts reportOptions, runtime reportRuntimeContext) (preparedReport, error) {
	if opts.rawStdin && opts.rawDefaultsOnly {
		return preparedReport{}, exitCode(errConflictingReportStdin, exitCodeUsage)
	}
	if strings.TrimSpace(opts.harness) == "" {
		return preparedReport{}, exitCode(errMissingReportHarness, exitCodeUsage)
	}
	harnessID, err := catalog.Parse(opts.harness)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalizing harness: %w", err), exitCodeUsage)
	}
	attrs, err := parseAttributes(opts.attributes)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	rawPayload, defaultsPayload, err := readStdinPayloadData(stdin, opts.rawStdin, opts.rawDefaultsOnly)
	if err != nil {
		return preparedReport{}, err
	}
	if !catalog.PayloadCompatible(harnessID, defaultsPayload) {
		return preparedReport{harness: harnessID, ignored: true}, nil
	}
	defaults, err := catalog.PayloadDefaults(harnessID, defaultsPayload)
	if err != nil {
		return preparedReport{}, fmt.Errorf("derive payload defaults: %w", err)
	}
	applyPayloadDefaults(&opts, attrs, defaults)
	applyReportRuntimeDefaults(&opts)
	if !strings.EqualFold(opts.evidence, "process") {
		translated := catalog.LifecycleFor(harnessID, opts.event, attrs)
		opts.event = translated.Event
		if opts.lifecycle == "" {
			opts.lifecycle = string(translated.Lifecycle)
		}
		if opts.presence == "" {
			opts.presence = string(translated.Presence)
		}
	}
	presence, err := registry.NormalizePresence(opts.presence)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalize presence: %w", err), exitCodeUsage)
	}
	activity, err := registry.NormalizeActivity(opts.activity)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalize activity: %w", err), exitCodeUsage)
	}
	if presence == registry.PresenceGone && activity != "" {
		return preparedReport{}, exitCode(errGonePresenceActivity, exitCodeUsage)
	}
	lifecycle, err := normalizeReportLifecycle(opts.lifecycle)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if lifecycle == registry.NativeLifecycleEnd && activity != "" {
		return preparedReport{}, exitCode(errGonePresenceActivity, exitCodeUsage)
	}
	observedAt, err := parseObservedAt(opts.observedAt)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if observedAt.IsZero() {
		observedAt = runtime.defaultObservedAt
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	activity, err = catalog.ActivityFromPayload(harnessID, opts.event, activity, defaultsPayload, observedAt)
	if err != nil {
		return preparedReport{}, fmt.Errorf("derive payload activity: %w", err)
	}
	sequence, sequenceSet, err := parseReportSequence(opts.sequence)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if presence == "" && activity == "" && lifecycle == "" && opts.event == "" && opts.sessionID == "" && opts.sessionPath == "" {
		return preparedReport{}, exitCode(errMissingReportIdentity, exitCodeUsage)
	}
	identity := registry.ObservationIdentity{CWD: "", Attributes: nil, SessionID: opts.sessionID, SessionPath: opts.sessionPath}
	var observation registry.Observation
	if strings.EqualFold(opts.evidence, "process") {
		if opts.pid <= 0 {
			return preparedReport{}, exitCode(errProcessEvidenceIdentity, exitCodeUsage)
		}
		if activity != "" {
			return preparedReport{}, exitCode(errProcessEvidenceActivity, exitCodeUsage)
		}
		if sequenceSet {
			return preparedReport{}, exitCode(errProcessEvidenceSequence, exitCodeUsage)
		}
		process := processEvidenceIdentity(opts, runtime.processes)
		if process == nil || !process.Complete() {
			return preparedReport{}, exitCode(errProcessEvidenceIdentity, exitCodeUsage)
		}
		present := presence != registry.PresenceGone
		observation = registry.Observation{Harness: harnessID, At: observedAt, Subject: identity, Evidence: &registry.Sighting{Process: *process, Present: present}}
	} else {
		observation = nativeReportObservation(harnessID, identity, opts, runtime, attrs, rawPayload, presence, activity, lifecycle, observedAt)
		if sequenceSet {
			observation.Report().Reporter.Sequence = &sequence
		}
	}
	if opts.cwd != "" || opts.projectRoot != "" || len(opts.resumeCommand) > 0 {
		observation.SetListing(&registry.Listing{ResumeCommand: append([]string(nil), opts.resumeCommand...), CWD: opts.cwd, ProjectRoot: opts.projectRoot})
	}
	observation = catalog.PrepareObservation(observation)
	if opts.detail != "" {
		if observation.Kind() != "report" {
			return preparedReport{}, exitCode(fmt.Errorf("%w: detail requires a native report", registry.ErrInvalidObservation), exitCodeUsage)
		}
		detail := registry.ActivityDetail(opts.detail)
		if opts.detail == "clear" {
			detail = ""
		}
		if detail != "" && !detail.ValidFor(activity) {
			return preparedReport{}, exitCode(fmt.Errorf("%w: detail does not match activity", registry.ErrInvalidObservation), exitCodeUsage)
		}
		observation.Report().Detail = &detail
	}
	if opts.detailObservedAt != "" {
		detailAt, err := parseObservedAt(opts.detailObservedAt)
		if err != nil || detailAt.IsZero() || detailAt.After(observedAt) {
			return preparedReport{}, exitCode(fmt.Errorf("%w: invalid detail timestamp", registry.ErrInvalidObservation), exitCodeUsage)
		}
		if observation.Kind() != "report" || observation.Report().Detail == nil || *observation.Report().Detail == "" {
			return preparedReport{}, exitCode(fmt.Errorf("%w: detail timestamp requires specific detail", registry.ErrInvalidObservation), exitCodeUsage)
		}
		observation.Report().DetailObservedAt = &detailAt
	}
	return preparedReport{harness: harnessID, observation: observation}, nil
}

func nativeReportObservation(
	harnessID registry.Harness,
	identity registry.ObservationIdentity,
	opts reportOptions,
	runtime reportRuntimeContext,
	attributes map[string]string,
	rawPayload json.RawMessage,
	presence registry.Presence,
	activity registry.Activity,
	lifecycle registry.NativeLifecycle,
	observedAt time.Time,
) registry.Observation {
	observation := registry.Observation{Harness: harnessID, At: observedAt, Subject: identity, Evidence: &registry.Report{Lifecycle: nil, Claim: nil, Activity: nil, Location: nil, Listing: nil, Reporter: registry.Reporter{Sequence: nil, Integration: opts.reporter, Version: opts.reporterVersion, MultiSession: opts.multiSession}, Event: opts.event, Process: reportProcessIdentity(harnessID, runtime.processes), Attributes: attributes, Payload: rawPayload, Detail: nil}}
	if lifecycle != "" {
		observation.Report().Lifecycle = &lifecycle
	}
	if !runtime.tmux.Empty() {
		location := runtime.tmux
		observation.SetLocation(&location)
	}
	if !runtime.multiplexer.Empty() {
		multiplexer := runtime.multiplexer
		observation.SetLocation(&multiplexer)
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

func processEvidenceIdentity(opts reportOptions, processes []processinfo.Process) *registry.ProcessIdentity {
	if opts.startIdentity != "" {
		return &registry.ProcessIdentity{Foreground: false, PID: opts.pid, PPID: opts.ppid, ProcessGroupID: opts.processGroupID, StartIdentity: opts.startIdentity, Executable: opts.executable, CWD: opts.cwd, TTY: opts.tty}
	}
	for _, process := range processes {
		if process.PID != opts.pid {
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

func appReportActivity(session registry.Session) string {
	return formatActivity(session.Activity())
}

func formatActivity(activity *registry.Activity) string {
	if activity == nil {
		return "null"
	}
	return string(*activity)
}

func (app *application) writeReportResult(session registry.Session, quiet bool) error {
	const (
		reportIDWidth            = 30
		reportAgentWidth         = 12
		reportPresenceWidth      = 10
		reportActivityWidth      = 12
		reportAuthoritativeWidth = 13
	)
	if app.outputJSON {
		return app.writeJSON(session)
	}
	if quiet {
		return nil
	}
	reportedActivity := "-"
	authoritative := "-"
	if native := session.Observations.Native; native != nil && native.Activity != nil {
		reportedActivity = string(*native.Activity)
		authoritative = "yes"
		if (catalog.Rules{}).Policy(session.Harness).Authority == registry.AuthorityScreen {
			authoritative = "no"
		}
	}
	return app.writeHumanTable(
		[]humanColumn{{heading: "ID", width: reportIDWidth}, {heading: "Agent", width: reportAgentWidth}, {heading: "Presence", width: reportPresenceWidth}, {heading: "Reported", width: reportActivityWidth}, {heading: "Effective", width: reportActivityWidth}, {heading: "Authoritative", width: reportAuthoritativeWidth}},
		[][]string{{session.ID, string(session.Harness), string(session.Presence()), reportedActivity, appReportActivity(session), authoritative}},
	)
}

func reportTmuxLocation(ctx context.Context, noTmux bool) registry.Location {
	if noTmux {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}
	}
	t, err := tmux.Current(ctx)
	if err != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}
	}
	return t
}

func reportMultiplexerLocation() registry.Location {
	if location := herdr.Current(); !location.Empty() {
		return location
	}
	return zellij.Current()
}

func reportProcessAncestors(ctx context.Context, pid int) []processinfo.Process {
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

func reportProcessIdentity(harnessID registry.Harness, processes []processinfo.Process) *registry.ProcessIdentity {
	for _, process := range processes {
		if !reportProcessMatchesHarness(process, harnessID) {
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

func reportProcessMatchesHarness(process processinfo.Process, expected registry.Harness) bool {
	if harnessID, ok := catalog.FromCommand(process.Executable); ok {
		return harnessID == expected
	}
	for _, arg := range process.Args[:min(reportProcessArgumentPrefixCount, len(process.Args))] {
		if harnessID, ok := catalog.FromCommand(arg); ok {
			return harnessID == expected
		}
	}
	return false
}

func parentProcessArgs(ctx context.Context) []string { return processArgs(ctx, os.Getppid()) }

func processArgs(ctx context.Context, pid int) []string {
	if pid <= 0 {
		return nil
	}
	if a := procProcessArgs(pid); len(a) > 0 {
		return a
	}
	return psProcessArgs(ctx, pid)
}

func procProcessArgs(pid int) []string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
}

func psProcessArgs(ctx context.Context, pid int) []string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(strings.TrimSpace(string(out)))
}

func applyPayloadDefaults(opts *reportOptions, a map[string]string, d harness.PayloadDefaults) {
	if opts.sessionID == "" {
		opts.sessionID = d.SessionID
	}
	if opts.sessionPath == "" {
		opts.sessionPath = d.SessionPath
	}
	if opts.event == "" {
		opts.event = d.Event
	}
	applyCWDDefault(opts, d.CWD)
	applyProjectRootDefault(opts, d.ProjectRoot)
	maps.Copy(a, d.Attributes)
}

func applyReportRuntimeDefaults(opts *reportOptions) {
	if opts.cwd == "" && opts.cwdAuto {
		if wd, err := os.Getwd(); err == nil {
			opts.cwd = wd
		}
	}
	if opts.projectRoot == "" && opts.projectRootAuto {
		opts.projectRoot = findProjectRoot(opts.cwd)
	}
}

func applyCWDDefault(opts *reportOptions, v string) {
	if v != "" && opts.cwdAuto && opts.cwd == "" {
		opts.cwd = v
		opts.projectRoot = findProjectRoot(v)
	}
}

func applyProjectRootDefault(opts *reportOptions, v string) {
	if v != "" && opts.projectRootAuto && opts.projectRoot == "" {
		opts.projectRoot = v
	}
}
