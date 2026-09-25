package cli

import (
	"context"
	"encoding/json"
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
	harnesspkg "github.com/zigai/aht/v2/internal/harness/catalog"

	"github.com/zigai/aht/v2/internal/processinfo"

	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
	"github.com/zigai/aht/v2/pkg/zellij"
)

type reportOptions struct {
	reporter        string
	reporterVersion int
	multiSession    bool
	harness         string
	presence        string
	activity        string
	lifecycle       string
	sessionID       string
	sessionPath     string
	cwd             string
	cwdAuto         bool
	projectRoot     string
	projectRootAuto bool
	pid             int
	ppid            int
	processGroupID  int
	startIdentity   string
	executable      string
	tty             string
	event           string
	observedAt      string
	sequence        string
	attributes      []string
	rawStdin        bool
	rawDefaultsOnly bool
	noTmux          bool
	quiet           bool
	resumeCommand   []string
	evidence        string
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
	return reportOptions{harness: firstEnv("AHT_HARNESS", "AGENT_HARNESS"), sessionID: firstEnv(harnesspkg.EnvNames(harness.EnvSessionID)...), sessionPath: firstEnv(harnesspkg.EnvNames(harness.EnvSessionPath)...), cwdAuto: true, projectRoot: firstEnv(harnesspkg.EnvNames(harness.EnvProjectRoot)...), pid: firstEnvInt(harnesspkg.EnvNames(harness.EnvPID)...), ppid: firstEnvInt("AHT_PPID", "AGENT_PPID"), tty: firstEnv("AHT_TTY", "TTY"), event: firstEnv(harnesspkg.EnvNames(harness.EnvEvent)...), sequence: firstEnv("AHT_SEQUENCE")}
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
		tmux:        reportTmuxContext(ctx, opts.noTmux),
		multiplexer: reportMultiplexerContext(),
		processes:   reportProcessAncestors(ctx, opts.pid),
	})
	if err != nil {
		return err
	}
	if prepared.ignored {
		if app.outputJSON {
			return app.writeJSON(map[string]string{statusCommandName: "ignored", "harness": string(prepared.harness)})
		}
		if opts.quiet {
			return nil
		}
		return app.writef("ignored %s report: hook payload does not match harness\n", prepared.harness)
	}
	session, err := app.registryStore().Observe(ctx, prepared.observation)
	if err != nil {
		return fmt.Errorf("recording observation: %w", err)
	}
	return app.writeReportResult(session, opts.quiet)
}

//nolint:gocognit,cyclop,nestif // report preparation validates independent evidence dimensions in order
func prepareReport(stdin io.Reader, options reportOptions, runtime reportRuntimeContext) (preparedReport, error) {
	if options.rawStdin && options.rawDefaultsOnly {
		return preparedReport{}, exitCode(errConflictingReportStdin, exitCodeUsage)
	}
	if strings.TrimSpace(options.harness) == "" {
		return preparedReport{}, exitCode(errMissingReportHarness, exitCodeUsage)
	}
	harness, err := harnesspkg.Normalize(options.harness)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalizing harness: %w", err), exitCodeUsage)
	}
	attrs, err := parseAttributes(options.attributes)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	rawPayload, defaultsPayload, err := readStdinPayloadData(stdin, options.rawStdin, options.rawDefaultsOnly)
	if err != nil {
		return preparedReport{}, err
	}
	if !harnesspkg.PayloadCompatibleWithHarness(harness, defaultsPayload) {
		return preparedReport{harness: harness, ignored: true}, nil
	}
	defaults, err := harnesspkg.DefaultsFromPayloadWithError(harness, defaultsPayload)
	if err != nil {
		return preparedReport{}, fmt.Errorf("derive payload defaults: %w", err)
	}
	applyPayloadDefaults(&options, attrs, defaults)
	applyReportRuntimeDefaults(&options)
	if !strings.EqualFold(options.evidence, "process") {
		translated := harnesspkg.LifecycleFor(harness, options.event, attrs)
		options.event = translated.Event
		if options.lifecycle == "" {
			options.lifecycle = string(translated.Lifecycle)
		}
		if options.presence == "" {
			options.presence = string(translated.Presence)
		}
	}
	presence, err := registry.NormalizePresence(options.presence)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalize presence: %w", err), exitCodeUsage)
	}
	activity, err := registry.NormalizeActivity(options.activity)
	if err != nil {
		return preparedReport{}, exitCode(fmt.Errorf("normalize activity: %w", err), exitCodeUsage)
	}
	if presence == registry.PresenceGone && activity != "" {
		return preparedReport{}, exitCode(errGonePresenceActivity, exitCodeUsage)
	}
	lifecycle, err := normalizeReportLifecycle(options.lifecycle)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if lifecycle == registry.NativeLifecycleEnd && activity != "" {
		return preparedReport{}, exitCode(errGonePresenceActivity, exitCodeUsage)
	}
	observedAt, err := parseObservedAt(options.observedAt)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if observedAt.IsZero() {
		observedAt = runtime.defaultObservedAt
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	sequence, sequenceSet, err := parseReportSequence(options.sequence)
	if err != nil {
		return preparedReport{}, exitCode(err, exitCodeUsage)
	}
	if presence == "" && activity == "" && lifecycle == "" && options.event == "" && options.sessionID == "" && options.sessionPath == "" {
		return preparedReport{}, exitCode(errMissingReportIdentity, exitCodeUsage)
	}
	identity := registry.ObservationIdentity{CWD: "", Attributes: nil, SessionID: options.sessionID, SessionPath: options.sessionPath}
	var observation registry.Observation
	if strings.EqualFold(options.evidence, "process") {
		if options.pid <= 0 {
			return preparedReport{}, exitCode(errProcessEvidenceIdentity, exitCodeUsage)
		}
		if activity != "" {
			return preparedReport{}, exitCode(errProcessEvidenceActivity, exitCodeUsage)
		}
		if sequenceSet {
			return preparedReport{}, exitCode(errProcessEvidenceSequence, exitCodeUsage)
		}
		process := processEvidenceIdentity(options, runtime.processes)
		if process == nil || !process.Complete() {
			return preparedReport{}, exitCode(errProcessEvidenceIdentity, exitCodeUsage)
		}
		present := presence != registry.PresenceGone
		observation = registry.Observation{Harness: harness, At: observedAt, Subject: identity, Evidence: &registry.Sighting{Process: *process, Present: present}}
	} else {
		observation = nativeReportObservation(harness, identity, options, runtime, attrs, rawPayload, presence, activity, lifecycle, observedAt)
		if sequenceSet {
			observation.Report().Reporter.Sequence = &sequence
		}
	}
	if options.cwd != "" || options.projectRoot != "" || len(options.resumeCommand) > 0 {
		observation.SetListing(&registry.Listing{ResumeCommand: append([]string(nil), options.resumeCommand...), CWD: options.cwd, ProjectRoot: options.projectRoot})
	}
	observation = harnesspkg.PrepareObservation(observation)
	return preparedReport{harness: harness, observation: observation}, nil
}

func nativeReportObservation(
	harness registry.Harness,
	identity registry.ObservationIdentity,
	options reportOptions,
	runtime reportRuntimeContext,
	attributes map[string]string,
	rawPayload json.RawMessage,
	presence registry.Presence,
	activity registry.Activity,
	lifecycle registry.NativeLifecycle,
	observedAt time.Time,
) registry.Observation {
	observation := registry.Observation{Harness: harness, At: observedAt, Subject: identity, Evidence: &registry.Report{Lifecycle: nil, Claim: nil, Activity: nil, Location: nil, Listing: nil, Reporter: registry.Reporter{Sequence: nil, Integration: options.reporter, Version: options.reporterVersion, MultiSession: options.multiSession}, Event: options.event, Process: reportProcessIdentity(harness, runtime.processes), Attributes: attributes, Payload: rawPayload}}
	if lifecycle != "" {
		observation.Report().Lifecycle = &lifecycle
	}
	if !runtime.tmux.Empty() {
		tmux := runtime.tmux
		observation.SetLocation(&tmux)
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

func processEvidenceIdentity(options reportOptions, processes []processinfo.Process) *registry.ProcessIdentity {
	if options.startIdentity != "" {
		return &registry.ProcessIdentity{Foreground: false, PID: options.pid, PPID: options.ppid, ProcessGroupID: options.processGroupID, StartIdentity: options.startIdentity, Executable: options.executable, CWD: options.cwd, TTY: options.tty}
	}
	for _, process := range processes {
		if process.PID != options.pid {
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
		if (harnesspkg.Rules{}).Policy(session.Harness).Authority == registry.AuthorityScreen {
			authoritative = "no"
		}
	}
	return app.writeHumanTable(
		[]humanColumn{{heading: "ID", width: reportIDWidth}, {heading: "Agent", width: reportAgentWidth}, {heading: "Presence", width: reportPresenceWidth}, {heading: "Reported", width: reportActivityWidth}, {heading: "Effective", width: reportActivityWidth}, {heading: "Authoritative", width: reportAuthoritativeWidth}},
		[][]string{{session.ID, string(session.Harness), string(session.Presence()), reportedActivity, appReportActivity(session), authoritative}},
	)
}

func reportTmuxContext(ctx context.Context, noTmux bool) registry.Location {
	if noTmux {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}
	}
	t, err := tmux.Current(ctx)
	if err != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}
	}
	return t
}

func reportMultiplexerContext() registry.Location {
	if ctx := herdr.Current(); !ctx.Empty() {
		return ctx
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

func reportProcessIdentity(harness registry.Harness, processes []processinfo.Process) *registry.ProcessIdentity {
	for _, process := range processes {
		if !reportProcessMatchesHarness(process, harness) {
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
	if harness, ok := harnesspkg.FromCommand(process.Executable); ok {
		return harness == expected
	}
	for _, arg := range process.Args[:min(reportProcessArgumentPrefixCount, len(process.Args))] {
		if harness, ok := harnesspkg.FromCommand(arg); ok {
			return harness == expected
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

func applyPayloadDefaults(o *reportOptions, a map[string]string, d harness.PayloadDefaults) {
	if o.sessionID == "" {
		o.sessionID = d.SessionID
	}
	if o.sessionPath == "" {
		o.sessionPath = d.SessionPath
	}
	if o.event == "" {
		o.event = d.Event
	}
	applyCWDDefault(o, d.CWD)
	applyProjectRootDefault(o, d.ProjectRoot)
	maps.Copy(a, d.Attributes)
}

func applyReportRuntimeDefaults(o *reportOptions) {
	if o.cwd == "" && o.cwdAuto {
		if wd, err := os.Getwd(); err == nil {
			o.cwd = wd
		}
	}
	if o.projectRoot == "" && o.projectRootAuto {
		o.projectRoot = findProjectRoot(o.cwd)
	}
}

func applyCWDDefault(o *reportOptions, v string) {
	if v != "" && o.cwdAuto && o.cwd == "" {
		o.cwd = v
		o.projectRoot = findProjectRoot(v)
	}
}

func applyProjectRootDefault(o *reportOptions, v string) {
	if v != "" && o.projectRootAuto && o.projectRoot == "" {
		o.projectRoot = v
	}
}
