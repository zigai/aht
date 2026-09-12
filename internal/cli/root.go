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
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/internal/config"
	"github.com/zigai/aht/internal/harness"
	harnesspkg "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/herdr"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
	"github.com/zigai/aht/pkg/zellij"
)

const (
	registryIDShortLength            = 8
	reportProcessArgumentPrefixCount = 4
	reportProcessAncestorLimit       = 16
	listCommandName                  = "list"
	statusCommandName                = "status"
	installCommandName               = "install"
	sigpipeExitCode                  = 141
	integrationsCommand              = "integrations"
	trackerCommand                   = "tracker"
	stateCommandName                 = "state"
	hookCommandName                  = "hook"
	hoursPerDay                      = 24
	jsonIndent                       = "  "
)

var (
	version                      = "dev"
	commit                       = "none"
	date                         = "unknown"
	configureCobraOnce           sync.Once
	errInvalidAttribute          = errors.New("invalid attribute")
	errInvalidListSort           = errors.New("invalid list sort")
	errUnexpectedReportArg       = errors.New("unexpected report argument")
	errMissingReportHarness      = errors.New("missing harness")
	errConflictingReportStdin    = errors.New("--raw-stdin and --raw-stdin-defaults-only cannot be used together")
	errMissingReportIdentity     = errors.New("missing report identity or transition")
	errDoctorFailed              = errors.New("doctor found errors")
	errInvalidObserveInterval    = errors.New("interval must be positive")
	errInvalidObserveGracePeriod = errors.New("grace period must be nonnegative")
	errGonePresenceActivity      = errors.New("gone presence cannot include activity")
	errProcessEvidenceSequence   = errors.New("process evidence cannot include sequence")
	errProcessEvidenceIdentity   = errors.New("process evidence requires pid and start identity")
	errProcessEvidenceActivity   = errors.New("process evidence cannot include activity")
	errManagedHookJSONRequired   = errors.New("hook commands require --json for their protocol response")
	errListSummaryFlag           = errors.New("--sort, --desc, and --absolute-time are not valid with --summary")
	errListAbsoluteJSON          = errors.New("--absolute-time cannot be used with --json")
)

type application struct {
	storePath          string
	configPath         string
	cfgLoaded          bool
	cfg                config.Config
	resolvedConfigPath string
	cfgErr             error
	outputJSON         bool
	stdout             io.Writer
	stderr             io.Writer
}

type reportOptions struct {
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
	tmux              registry.TmuxContext
	multiplexer       registry.MultiplexerContext
	processes         []processinfo.Process
	defaultObservedAt time.Time
}

type listOptions struct {
	harness, presence, activity, tmuxSession, multiplexerSession, sortBy string
	summary, absoluteTime, absoluteSet, sortSet, desc, descSet, full     bool
}

type sessionCompareFunc func(registry.Session, registry.Session) int

func (app *application) loadConfig() (config.Config, error) {
	if app.cfgLoaded {
		return app.cfg, app.cfgErr
	}
	if app.configPath == "" {
		targetPath := config.DefaultPath()
		if _, statErr := os.Stat(targetPath); errors.Is(statErr, os.ErrNotExist) {
			// Best-effort auto-creation on first run. If this fails (e.g. read-only filesystem),
			// proceed without failing startup; config.Load will use built-in defaults.
			_, _ = config.EnsureConfigFile(targetPath)
		}
	}
	cfg, resolved, err := config.Load(app.configPath)
	app.cfg = cfg
	app.resolvedConfigPath = resolved
	app.cfgErr = err
	app.cfgLoaded = true
	return app.cfg, app.cfgErr
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := executeCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	if code != 0 {
		os.Exit(code)
	}
}

func NewRootCommand(stdout io.Writer, stderr io.Writer) *cobra.Command {
	configureCobra()
	return (&application{stdout: stdout, stderr: stderr}).newRootCommand()
}

func configureCobra() {
	configureCobraOnce.Do(func() {
		cobra.EnableCommandSorting = false
	})
}

func (app *application) newRootCommand() *cobra.Command {
	var showVersion bool
	root := &cobra.Command{Use: "aht", Short: "Track local coding-agent sessions and where they are running", SilenceErrors: true, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		if showVersion {
			if app.outputJSON {
				return app.writeJSON(map[string]string{"version": version, "commit": commit, "built": date})
			}
			return app.writef("aht %s (commit: %s, built: %s)\n", version, commit, date)
		}
		return cmd.Help()
	}, CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true}}
	root.SetOut(app.stdout)
	root.SetErr(app.stderr)
	root.PersistentFlags().StringVar(&app.storePath, "store", "", "registry state file path")
	root.PersistentFlags().StringVar(&app.configPath, "config", "", "config file path")
	root.PersistentFlags().BoolVar(&app.outputJSON, "json", false, "emit JSON (JSON Lines for streams)")
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "print version")
	root.AddCommand(
		app.newListCommand(),
		app.newWatchCommand(),
		app.newInfoCommand(),
		app.newStopCommand(),
		app.newManageCommand(),
		app.newWireCommand(),
		app.newHookCommand(),
		app.newReportCommand(),
	)
	return root
}

func (app *application) newManageCommand() *cobra.Command {
	command := &cobra.Command{Use: "manage", Short: "Manage setup, integrations, tracking, and state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}}
	command.AddCommand(
		app.newSetupCommand(),
		app.newUpgradeCommand(),
		app.newIntegrationsCommand(),
		app.newTrackerCommand(),
		app.newStateCommand(),
		app.newDoctorCommand(),
		app.newManageConfigCommand(),
	)
	return command
}

func executeCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	//nolint:contextcheck // Cobra propagates ExecuteContext to every command operation.
	root := NewRootCommand(stdout, stderr)
	root.SetArgs(args)
	root.SetIn(stdin)
	if err := root.ExecuteContext(ctx); err != nil {
		var status interface{ ExitCode() int }
		if errors.As(err, &status) {
			if status.ExitCode() != sigpipeExitCode {
				_, _ = fmt.Fprintln(stderr, err)
			}
			return status.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func (app *application) resolvedStorePath() string {
	if app.storePath != "" {
		return app.storePath
	}
	return registry.DefaultStorePath()
}

func (app *application) store() *registry.FileStore {
	return registry.NewFileStore(app.resolvedStorePath())
}

func (app *application) registryStore() *client.Client {
	return client.New(client.Config{StorePath: app.resolvedStorePath()})
}

func (app *application) writeJSON(value any) error {
	e := json.NewEncoder(app.stdout)
	e.SetIndent("", jsonIndent)
	if err := e.Encode(value); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}
	return nil
}

func (app *application) writeJSONLine(value any) error {
	e := json.NewEncoder(app.stdout)
	if err := e.Encode(value); err != nil {
		return fmt.Errorf("writing JSON line: %w", err)
	}
	return nil
}

func (app *application) writef(format string, args ...any) error {
	if _, err := fmt.Fprintf(app.stdout, format, args...); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

func (app *application) writeln(args ...any) error {
	if _, err := fmt.Fprintln(app.stdout, args...); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return nil
}

func (app *application) warnf(format string, args ...any) {
	if app.stderr != nil {
		_, _ = fmt.Fprintf(app.stderr, format, args...)
	}
}

func (app *application) newRegistryPathCommand() *cobra.Command {
	return &cobra.Command{Use: "path", Short: "Print the registry state file path", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if app.outputJSON {
			return app.writeJSON(map[string]string{"path": app.resolvedStorePath()})
		}
		return app.writeln(app.resolvedStorePath())
	}}
}

func (app *application) newReportCommand() *cobra.Command {
	options := defaultReportOptionsFromEnv()
	cmd := &cobra.Command{Use: "report [harness]", Short: "Record a harness observation", Hidden: true, SilenceUsage: true, Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if options.harness != "" {
				return fmt.Errorf("%w: harness already set", errUnexpectedReportArg)
			}
			options.harness = args[0]
		}
		if cmd.Flags().Changed("cwd") {
			options.cwdAuto = false
		}
		if cmd.Flags().Changed("project-root") {
			options.projectRootAuto = false
		}
		return app.runReport(cmd.Context(), cmd.InOrStdin(), options)
	}}
	f := cmd.Flags()
	f.StringVar(&options.presence, "presence", options.presence, "presence: live, gone, unknown")
	f.StringVar(&options.activity, "activity", options.activity, "reported activity hint: running, waiting, idle, unknown; screen-authority agents derive effective activity from their pane")
	f.StringVar(&options.lifecycle, "lifecycle", options.lifecycle, "native lifecycle: start, resume, end")
	_ = f.MarkHidden("lifecycle")
	f.StringVar(&options.sessionID, "session-id", options.sessionID, "harness session id")
	f.StringVar(&options.sessionPath, "session-path", options.sessionPath, "harness session file path")
	f.StringVar(&options.cwd, "cwd", options.cwd, "agent current working directory")
	f.StringVar(&options.projectRoot, "project-root", options.projectRoot, "project root")
	f.IntVar(&options.pid, "pid", options.pid, "agent process id")
	f.IntVar(&options.ppid, "ppid", options.ppid, "agent parent process id")
	f.IntVar(&options.processGroupID, "process-group-id", options.processGroupID, "agent process group id")
	f.StringVar(&options.startIdentity, "start-identity", options.startIdentity, "process start identity")
	f.StringVar(&options.executable, "executable", options.executable, "resolved executable path")
	f.StringVar(&options.tty, "tty", options.tty, "agent tty")
	f.StringVar(&options.event, "event", options.event, "native harness event name")
	f.StringVar(&options.observedAt, "observed-at", options.observedAt, "RFC3339 timestamp")
	f.StringVar(&options.sequence, "sequence", options.sequence, "strictly increasing integration report sequence")
	f.StringArrayVar(&options.attributes, "attribute", nil, "extra key=value attribute")
	f.StringArrayVar(&options.resumeCommand, "resume-command", nil, "resume command argv item, repeatable")
	f.StringVar(&options.evidence, "evidence", options.evidence, "evidence kind (managed shims)")
	f.BoolVar(&options.rawStdin, "raw-stdin", false, "store stdin as raw hook payload")
	f.BoolVar(&options.rawDefaultsOnly, "raw-stdin-defaults-only", false, "read stdin for defaults without storing raw payload")
	f.BoolVar(&options.noTmux, "no-tmux", false, "do not collect tmux context")
	_ = f.MarkHidden("evidence")
	f.BoolVar(&options.quiet, "quiet", false, "suppress human-readable output")
	return cmd
}

func defaultReportOptionsFromEnv() reportOptions {
	return reportOptions{harness: firstEnv("AHT_HARNESS", "AGENT_HARNESS"), sessionID: firstEnv(harnesspkg.EnvNames(harness.EnvSessionID)...), sessionPath: firstEnv(harnesspkg.EnvNames(harness.EnvSessionPath)...), cwdAuto: true, projectRoot: firstEnv(harnesspkg.EnvNames(harness.EnvProjectRoot)...), pid: firstEnvInt(harnesspkg.EnvNames(harness.EnvPID)...), ppid: firstEnvInt("AHT_PPID", "AGENT_PPID"), tty: firstEnv("AHT_TTY", "TTY"), event: firstEnv(harnesspkg.EnvNames(harness.EnvEvent)...), sequence: firstEnv("AHT_SEQUENCE")}
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

func (app *application) runReport(ctx context.Context, stdin io.Reader, options reportOptions) error {
	prepared, err := prepareReport(stdin, options, reportRuntimeContext{
		tmux:        reportTmuxContext(ctx, options.noTmux),
		multiplexer: reportMultiplexerContext(),
		processes:   reportProcessAncestors(ctx, options.pid),
	})
	if err != nil {
		return err
	}
	if prepared.ignored {
		if app.outputJSON {
			return app.writeJSON(map[string]string{statusCommandName: "ignored", "harness": string(prepared.harness)})
		}
		if options.quiet {
			return nil
		}
		return app.writef("ignored %s report: hook payload does not match harness\n", prepared.harness)
	}
	session, err := app.registryStore().Observe(ctx, prepared.observation)
	if err != nil {
		return fmt.Errorf("recording observation: %w", err)
	}
	return app.writeReportResult(session, options.quiet)
}

//nolint:gocognit,cyclop,nestif // report preparation validates independent evidence dimensions in order
func prepareReport(stdin io.Reader, options reportOptions, runtime reportRuntimeContext) (preparedReport, error) {
	if options.rawStdin && options.rawDefaultsOnly {
		return preparedReport{}, errConflictingReportStdin
	}
	if strings.TrimSpace(options.harness) == "" {
		return preparedReport{}, errMissingReportHarness
	}
	harness, err := harnesspkg.Normalize(options.harness)
	if err != nil {
		return preparedReport{}, fmt.Errorf("normalizing harness: %w", err)
	}
	attrs, err := parseAttributes(options.attributes)
	if err != nil {
		return preparedReport{}, err
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
	applyNativeLifecycleDefaults(&options, attrs)
	presence, err := registry.NormalizePresence(options.presence)
	if err != nil {
		return preparedReport{}, fmt.Errorf("normalize presence: %w", err)
	}
	activity, err := registry.NormalizeActivity(options.activity)
	if err != nil {
		return preparedReport{}, fmt.Errorf("normalize activity: %w", err)
	}
	if presence == registry.PresenceGone && activity != "" {
		return preparedReport{}, errGonePresenceActivity
	}
	lifecycle, err := normalizeReportLifecycle(options.lifecycle)
	if err != nil {
		return preparedReport{}, err
	}
	if lifecycle == registry.NativeLifecycleEnd && activity != "" {
		return preparedReport{}, errGonePresenceActivity
	}
	observedAt, err := parseObservedAt(options.observedAt)
	if err != nil {
		return preparedReport{}, err
	}
	if observedAt.IsZero() {
		observedAt = runtime.defaultObservedAt
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	sequence, sequenceSet, err := parseReportSequence(options.sequence)
	if err != nil {
		return preparedReport{}, err
	}
	if presence == "" && activity == "" && lifecycle == "" && options.event == "" && options.sessionID == "" && options.sessionPath == "" {
		return preparedReport{}, errMissingReportIdentity
	}
	identity := registry.ObservationIdentity{SessionID: options.sessionID, SessionPath: options.sessionPath}
	var observation registry.Observation
	if strings.EqualFold(options.evidence, "process") {
		if options.pid <= 0 {
			return preparedReport{}, errProcessEvidenceIdentity
		}
		if activity != "" {
			return preparedReport{}, errProcessEvidenceActivity
		}
		if sequenceSet {
			return preparedReport{}, errProcessEvidenceSequence
		}
		process := processEvidenceIdentity(options, runtime.processes)
		if process == nil || !process.Complete() {
			return preparedReport{}, errProcessEvidenceIdentity
		}
		present := presence != registry.PresenceGone
		observation = registry.Observation{
			Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
			Harness: harness, Identity: identity, ProcessPresent: &present, Process: process, ObservedAt: observedAt,
		}
	} else {
		observation = nativeReportObservation(harness, identity, options, runtime, attrs, rawPayload, presence, activity, lifecycle, observedAt)
		if sequenceSet {
			observation.Sequence = &sequence
		}
	}
	if options.cwd != "" || options.projectRoot != "" || len(options.resumeCommand) > 0 {
		observation.Catalog = &registry.CatalogMetadata{ResumeCommand: append([]string(nil), options.resumeCommand...), CWD: options.cwd, ProjectRoot: options.projectRoot}
	}
	observation = harnesspkg.WithResumeCommand(observation)
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
	observation := registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: harness, Identity: identity, NativeEvent: options.event, Attributes: attributes,
		RawPayload: rawPayload, Process: reportProcessIdentity(harness, runtime.processes), ObservedAt: observedAt,
	}
	if lifecycle != "" {
		observation.Lifecycle = &lifecycle
	}
	if agentstate.PolicyFor(harness).Primary == agentstate.AuthorityScreen {
		authoritative := false
		observation.ActivityAuthoritative = &authoritative
	}
	if !runtime.tmux.Empty() {
		tmux := runtime.tmux
		observation.Tmux = &tmux
	}
	if !runtime.multiplexer.Empty() {
		multiplexer := runtime.multiplexer
		observation.Multiplexer = &multiplexer
	}
	if presence != "" {
		observation.Presence = &presence
	}
	if activity != "" {
		observation.Activity = &activity
	}
	if observation.NativeEvent == "" && (presence != "" || activity != "" || lifecycle != "") {
		observation.NativeEvent = "cli"
	}
	return observation
}

func applyNativeLifecycleDefaults(options *reportOptions, attributes map[string]string) {
	if strings.EqualFold(options.evidence, "process") {
		return
	}

	event := firstReportAttribute(attributes,
		"pi_event",
		"omp_event",
		"codex_hook_event",
		"claude_hook_event",
		"cursor_hook_event",
		"copilot_hook_event",
		"droid_hook_event",
		"kimi_code_hook_event",
		"grok_hook_event",
		"goose_event",
	)
	if strings.TrimSpace(options.event) != "" {
		event = options.event
	} else if event != "" {
		options.event = event
	}

	switch normalizedNativeLifecycleEvent(event) {
	case "start":
		lifecycle := string(registry.NativeLifecycleStart)
		if nativeLifecycleSourceIsResume(attributes) {
			lifecycle = string(registry.NativeLifecycleResume)
		}
		applyLifecyclePresence(options, lifecycle, string(registry.PresenceLive))
	case "resume":
		applyLifecyclePresence(options, string(registry.NativeLifecycleResume), string(registry.PresenceLive))
	case "end":
		applyLifecyclePresence(options, string(registry.NativeLifecycleEnd), string(registry.PresenceGone))
	}
}

func applyLifecyclePresence(options *reportOptions, lifecycle, presence string) {
	if options.lifecycle == "" {
		options.lifecycle = lifecycle
	}
	if options.presence == "" {
		options.presence = presence
	}
}

func normalizedNativeLifecycleEvent(event string) string {
	normalized := strings.Map(func(character rune) rune {
		switch {
		case character >= 'A' && character <= 'Z':
			return character + ('a' - 'A')
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			return character
		default:
			return -1
		}
	}, strings.TrimSpace(event))

	switch normalized {
	case "sessionstart", "sessioncreated", "onsessionstart":
		return "start"
	case "sessionswitch", "sessionbranch", "sessiontree":
		return "resume"
	case "sessionend", "sessionshutdown", "sessiondeleted", "onsessionfinalize":
		return "end"
	default:
		return ""
	}
}

func nativeLifecycleSourceIsResume(attributes map[string]string) bool {
	source := firstReportAttribute(attributes,
		"codex_start_source",
		"claude_start_source",
		"cursor_start_source",
		"copilot_start_source",
		"droid_source",
		"kimi_code_start_source",
		"grok_start_source",
		"goose_start_source",
		"pi_reason",
		"omp_reason",
		"omp_approval_reason",
		"source",
		"reason",
	)
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "resume", "resumed":
		return true
	default:
		return false
	}
}

func firstReportAttribute(attributes map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attributes[key]); value != "" {
			return value
		}
	}
	return ""
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
		return &registry.ProcessIdentity{PID: options.pid, PPID: options.ppid, ProcessGroupID: options.processGroupID, StartIdentity: options.startIdentity, Executable: options.executable, CWD: options.cwd, TTY: options.tty}
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
	if session.Activity == nil {
		return "null"
	}
	return string(*session.Activity)
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
		if native.ActivityAuthoritative != nil && !*native.ActivityAuthoritative {
			authoritative = "no"
		}
	}
	return app.writeHumanTable(
		[]humanColumn{{heading: "ID", width: reportIDWidth}, {heading: "Agent", width: reportAgentWidth}, {heading: "Presence", width: reportPresenceWidth}, {heading: "Reported", width: reportActivityWidth}, {heading: "Effective", width: reportActivityWidth}, {heading: "Authoritative", width: reportAuthoritativeWidth}},
		[][]string{{session.ID, string(session.Harness), string(session.Presence), reportedActivity, appReportActivity(session), authoritative}},
	)
}

func reportTmuxContext(ctx context.Context, noTmux bool) registry.TmuxContext {
	if noTmux {
		return registry.TmuxContext{}
	}
	t, err := tmux.Current(ctx)
	if err != nil {
		return registry.TmuxContext{}
	}
	return t
}

func reportMultiplexerContext() registry.MultiplexerContext {
	if context := herdr.Current(); !context.Empty() {
		return context
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

func (app *application) newListCommand() *cobra.Command {
	o := listOptions{}
	cmd := &cobra.Command{Use: listCommandName, Short: "Show known sessions", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		cfg, err := app.loadConfig()
		if err != nil {
			return err
		}
		applyListConfig(&o, c, cfg)
		return app.runList(c.Context(), o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.harness, "agent", "", "filter by agent")
	f.StringVar(&o.presence, "presence", "", "filter by presence")
	f.StringVar(&o.activity, "activity", "", "filter by activity")
	f.StringVar(&o.tmuxSession, "tmux-session", "", "filter by tmux session")
	f.StringVar(&o.multiplexerSession, "multiplexer-session", "", "filter by multiplexer session")
	f.StringVar(&o.sortBy, "sort", "", "sort by: multiplexer, tmux, updated, presence-changed, activity-changed, created, harness, presence, activity, cwd, id")
	f.BoolVar(&o.summary, "summary", false, "summarize agent counts by multiplexer session")
	f.BoolVar(&o.absoluteTime, "absolute-time", false, "show full timestamps")
	f.BoolVar(&o.desc, "desc", false, "sort descending")
	f.BoolVar(&o.full, "full", false, "show complete values using an adaptive layout")
	return cmd
}

func applyListConfig(o *listOptions, cmd *cobra.Command, cfg config.Config) {
	f := cmd.Flags()
	if !f.Changed("presence") && cfg.UI.DefaultPresence != "" {
		o.presence = cfg.UI.DefaultPresence
	}
	if !f.Changed("sort") && cfg.UI.Sort != "" {
		o.sortBy = cfg.UI.Sort
	}
	o.sortSet = f.Changed("sort")
	if !f.Changed("desc") && cfg.UI.SortDesc != nil {
		o.desc = *cfg.UI.SortDesc
	}
	o.descSet = f.Changed("desc")
	if !f.Changed("absolute-time") {
		if (cfg.UI.AbsoluteTime != nil && *cfg.UI.AbsoluteTime) ||
			cfg.UI.TimeFormat == "absolute" || cfg.UI.TimeFormat == "iso8601" {
			o.absoluteTime = true
		}
	} else {
		o.absoluteSet = f.Changed("absolute-time")
	}
}

func (app *application) runList(ctx context.Context, o listOptions) error {
	if err := app.validateListOptions(o); err != nil {
		return err
	}
	if o.summary {
		return app.runListSummary(ctx, o)
	}
	return app.runListSessions(ctx, o)
}

func (app *application) validateListOptions(options listOptions) error {
	if app.outputJSON && options.absoluteSet {
		return errListAbsoluteJSON
	}
	if options.summary && (options.absoluteSet || options.sortSet || options.descSet) {
		return errListSummaryFlag
	}
	return nil
}

func buildFilter(o listOptions) (registry.Filter, error) {
	f := registry.Filter{TmuxSession: o.tmuxSession, MultiplexerSession: o.multiplexerSession}
	if o.harness != "" {
		h, e := harnesspkg.Normalize(o.harness)
		if e != nil {
			return f, fmt.Errorf("normalize harness: %w", e)
		}
		f.Harness = h
	}
	if o.presence != "" {
		if strings.EqualFold(o.presence, "all") {
			f.Presence = ""
		} else {
			p, e := registry.NormalizePresence(o.presence)
			if e != nil {
				return f, fmt.Errorf("normalize presence: %w", e)
			}
			f.Presence = p
		}
	}
	if o.activity != "" {
		a, e := registry.NormalizeActivity(o.activity)
		if e != nil {
			return f, fmt.Errorf("normalize activity: %w", e)
		}
		f.Activity = a
	}
	return f, nil
}

func (app *application) runListSessions(ctx context.Context, o listOptions) error {
	var err error
	o, err = normalizedListOptions(o)
	if err != nil {
		return err
	}
	f, e := buildFilter(o)
	if e != nil {
		return e
	}
	ss, e := app.registryStore().List(ctx, f)
	if e != nil {
		return fmt.Errorf("listing sessions: %w", e)
	}
	ss = applyConfigFilter(ss, app.cfg.Filter, o.harness)
	if e = sortListSessions(ss, o); e != nil {
		return e
	}
	if app.outputJSON {
		return app.writeJSON(ss)
	}
	now := time.Now().UTC()
	var displayIDs map[string]string
	if !o.full {
		displayIDs = abbreviatedRegistryIDs(ss)
	}
	rows := make([][]string, 0, len(ss))
	for _, s := range ss {
		id := s.ID
		if !o.full {
			id = displayIDs[s.ID]
		}
		rows = append(rows, []string{
			id, string(s.Harness), sessionDisplayLabel(s), string(s.Presence), listActivity(s),
			watchMultiplexerLabel(s.Multiplexer), formatHumanPath(s.CWD), formatUpdatedAt(s.UpdatedAt, now, o.absoluteTime),
		})
	}
	maxWidth := app.maxLineWidth()
	if o.full {
		columns, fits := listFullTableColumns(rows, maxWidth)
		if !fits {
			return app.writeStackedHumanRows(columns, rows)
		}
		return app.writeWrappedHumanTable(columns, rows)
	}
	return app.writeHumanTable(listTableColumns(rows, maxWidth), rows)
}

func matchPathPattern(path, pattern string) bool {
	if pattern == "" || path == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanPattern := filepath.Clean(pattern)
	if cleanPath == cleanPattern {
		return true
	}
	if matchGlobPattern(cleanPath, cleanPattern, pattern) {
		return true
	}
	return matchPrefixOrWildcard(cleanPath, pattern)
}

func matchGlobPattern(cleanPath, cleanPattern, pattern string) bool {
	if matched, err := filepath.Match(pattern, cleanPath); err == nil && matched {
		return true
	}
	if matched, err := filepath.Match(cleanPattern, cleanPath); err == nil && matched {
		return true
	}
	return false
}

func matchPrefixOrWildcard(cleanPath, pattern string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(pattern, "/*"), "/")
	if cleanPath == trimmed || strings.HasPrefix(cleanPath, trimmed+string(filepath.Separator)) {
		return true
	}
	if strings.Contains(pattern, "**") {
		sub := strings.Trim(strings.Trim(pattern, "*"), string(filepath.Separator))
		if sub != "" && (strings.Contains(cleanPath, string(filepath.Separator)+sub+string(filepath.Separator)) ||
			strings.HasSuffix(cleanPath, string(filepath.Separator)+sub) ||
			strings.HasPrefix(cleanPath, sub+string(filepath.Separator)) ||
			cleanPath == sub) {
			return true
		}
	}
	return false
}

func sessionMatchesIgnorePaths(s registry.Session, paths []string) bool {
	if s.CWD == "" || len(paths) == 0 {
		return false
	}
	for _, pat := range paths {
		if matchPathPattern(s.CWD, pat) {
			return true
		}
	}
	return false
}

func applyConfigFilter(sessions []registry.Session, filter config.FilterConfig, agentExplicit string) []registry.Session {
	if len(sessions) == 0 {
		return sessions
	}
	ignoreHarnessMap := make(map[string]struct{}, len(filter.IgnoreHarnesses))
	for _, h := range filter.IgnoreHarnesses {
		norm := strings.ToLower(strings.TrimSpace(h))
		if norm != "" {
			ignoreHarnessMap[norm] = struct{}{}
		}
	}

	result := make([]registry.Session, 0, len(sessions))
	for _, s := range sessions {
		if agentExplicit == "" && len(ignoreHarnessMap) > 0 {
			if _, ignored := ignoreHarnessMap[strings.ToLower(string(s.Harness))]; ignored {
				continue
			}
		}
		if sessionMatchesIgnorePaths(s, filter.IgnorePaths) {
			continue
		}
		result = append(result, s)
	}
	return result
}

func listColumnMetrics(rows [][]string) ([]string, []int) {
	headings := []string{"ID", "Agent", "Session", "Presence", "Activity", "Location", "CWD", "Updated"}
	maxLen := make([]int, len(headings))
	for i, h := range headings {
		maxLen[i] = text.StringWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(maxLen) {
				maxLen[i] = max(maxLen[i], text.StringWidth(cell))
			}
		}
	}
	return headings, maxLen
}

func listTableColumns(rows [][]string, maxWidth int) []humanColumn {
	const (
		maxLocationColumnWidth = 24
	)
	if maxWidth <= 0 {
		maxWidth = humanLineWidth
	}
	headings, maxLen := listColumnMetrics(rows)

	idWidth := maxLen[0]
	agentWidth := maxLen[1]
	presenceWidth := maxLen[3]
	activityWidth := maxLen[4]
	locationWidth := max(len("Location"), min(maxLen[5], maxLocationColumnWidth))
	updatedWidth := max(len("Updated"), maxLen[7])

	gapsTotal := (len(headings) - 1) * humanColumnGap
	fixedTotal := idWidth + agentWidth + presenceWidth + activityWidth + locationWidth + updatedWidth + gapsTotal

	sessionNeeded := max(len("Session"), maxLen[2])
	cwdNeeded := max(len("CWD"), maxLen[6])
	available := maxWidth - fixedTotal

	var sessionWidth, cwdWidth int
	switch {
	case available >= sessionNeeded+cwdNeeded:
		sessionWidth = sessionNeeded
		cwdWidth = cwdNeeded
	case available > 0:
		totalNeeded := sessionNeeded + cwdNeeded
		sessionWidth = max(len("Session"), (available*sessionNeeded)/totalNeeded)
		cwdWidth = max(len("CWD"), available-sessionWidth)
		if cwdWidth > cwdNeeded {
			sessionWidth += cwdWidth - cwdNeeded
			cwdWidth = cwdNeeded
		}
	default:
		sessionWidth = len("Session")
		cwdWidth = len("CWD")
	}

	return []humanColumn{
		{heading: "ID", width: idWidth},
		{heading: "Agent", width: agentWidth},
		{heading: "Session", width: sessionWidth},
		{heading: "Presence", width: presenceWidth},
		{heading: "Activity", width: activityWidth},
		{heading: "Location", width: locationWidth},
		{heading: "CWD", width: cwdWidth},
		{heading: "Updated", width: updatedWidth},
	}
}

func listFullTableColumns(rows [][]string, maxWidth int) ([]humanColumn, bool) {
	const (
		sessionMinWidth = 24
		cwdMinWidth     = 20
	)
	headings, maxLen := listColumnMetrics(rows)
	sessionNeeded := maxLen[2]
	cwdNeeded := maxLen[6]
	sessionWidth := min(sessionNeeded, max(len("Session"), sessionMinWidth))
	cwdWidth := min(cwdNeeded, max(len("CWD"), cwdMinWidth))
	fixedWidth := maxLen[0] + maxLen[1] + maxLen[3] + maxLen[4] + maxLen[5] + maxLen[7]
	available := maxWidth - fixedWidth - (len(headings)-1)*humanColumnGap
	fits := available >= sessionWidth+cwdWidth
	if fits {
		sessionWidth, cwdWidth = allocateFullListWidths(
			sessionWidth,
			cwdWidth,
			sessionNeeded,
			cwdNeeded,
			available,
		)
	}
	return []humanColumn{
		{heading: "ID", width: maxLen[0], wrap: wrapHumanIdentifier},
		{heading: "Agent", width: maxLen[1]},
		{heading: "Session", width: sessionWidth, wrap: wrapHumanSession},
		{heading: "Presence", width: maxLen[3]},
		{heading: "Activity", width: maxLen[4]},
		{heading: "Location", width: maxLen[5], wrap: wrapHumanIdentifier},
		{heading: "CWD", width: cwdWidth, wrap: wrapHumanPath},
		{heading: "Updated", width: maxLen[7]},
	}, fits
}

func allocateFullListWidths(
	sessionWidth int,
	cwdWidth int,
	sessionNeeded int,
	cwdNeeded int,
	available int,
) (int, int) {
	if available >= sessionNeeded+cwdNeeded {
		return sessionNeeded, cwdNeeded
	}
	extra := available - sessionWidth - cwdWidth
	sessionUnmet := sessionNeeded - sessionWidth
	cwdUnmet := cwdNeeded - cwdWidth
	totalUnmet := sessionUnmet + cwdUnmet
	if extra <= 0 || totalUnmet == 0 {
		return sessionWidth, cwdWidth
	}
	sessionAdd := min(sessionUnmet, extra*sessionUnmet/totalUnmet)
	cwdAdd := min(cwdUnmet, extra-sessionAdd)
	remaining := extra - sessionAdd - cwdAdd
	additionalSession := min(sessionUnmet-sessionAdd, remaining)
	sessionAdd += additionalSession
	remaining -= additionalSession
	cwdAdd += min(cwdUnmet-cwdAdd, remaining)
	return sessionWidth + sessionAdd, cwdWidth + cwdAdd
}

func normalizedListOptions(options listOptions) (listOptions, error) {
	if options.sortSet && strings.TrimSpace(options.sortBy) == "" {
		return options, fmt.Errorf("%w: empty value", errInvalidListSort)
	}
	return options, nil
}

func listActivity(session registry.Session) string {
	if session.Presence == registry.PresenceGone {
		return "-"
	}
	return appReportActivity(session)
}

func shortRegistryID(id string) string {
	separator := strings.LastIndexByte(id, '-')
	if separator < 0 || len(id)-separator-1 <= registryIDShortLength {
		return id
	}
	return id[:separator+1] + id[separator+1:separator+1+registryIDShortLength]
}

func abbreviatedRegistryIDs(sessions []registry.Session) map[string]string {
	type idParts struct {
		prefix string
		suffix string
	}
	parts := make(map[string]idParts, len(sessions))
	for _, session := range sessions {
		p, s := splitRegistryID(session.ID)
		parts[session.ID] = idParts{prefix: p, suffix: s}
	}

	result := make(map[string]string, len(sessions))
	for _, session := range sessions {
		part := parts[session.ID]
		if len(part.suffix) <= registryIDShortLength {
			result[session.ID] = session.ID
			continue
		}
		length := registryIDShortLength
		for _, other := range sessions {
			if session.ID == other.ID {
				continue
			}
			otherPart := parts[other.ID]
			if part.prefix != otherPart.prefix {
				continue
			}
			common := commonPrefixLength(part.suffix, otherPart.suffix)
			if common >= length {
				length = min(common+1, len(part.suffix))
			}
		}
		result[session.ID] = part.prefix + part.suffix[:length]
	}
	return result
}

func splitRegistryID(id string) (string, string) {
	separator := strings.LastIndexByte(id, '-')
	if separator < 0 {
		return "", id
	}
	return id[:separator+1], id[separator+1:]
}

func commonPrefixLength(left string, right string) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

func sessionDisplayLabel(session registry.Session) string {
	if session.SessionID != "" {
		return session.SessionID
	}
	if session.SessionPath != "" {
		return formatSessionPathLabel(session.SessionPath)
	}
	if session.Multiplexer.PaneID != "" {
		return session.Multiplexer.PaneID
	}
	if session.Process != nil && session.Process.PID > 0 {
		return fmt.Sprintf("pid:%d", session.Process.PID)
	}
	return shortRegistryID(session.ID)
}

func formatSessionPathLabel(path string) string {
	base := filepath.Base(path)
	trimmed := base
	for _, ext := range []string{".jsonl", ".json"} {
		if cut, ok := strings.CutSuffix(trimmed, ext); ok {
			trimmed = cut
			break
		}
	}
	if separator := strings.LastIndexByte(trimmed, '_'); separator >= 0 && separator+1 < len(trimmed) {
		suffix := trimmed[separator+1:]
		if len(suffix) >= 8 || strings.Contains(suffix, "-") {
			return suffix
		}
	}
	return base
}

func (app *application) runListSummary(ctx context.Context, o listOptions) error {
	f, e := buildFilter(o)
	if e != nil {
		return e
	}
	s, e := app.registryStore().SummaryByTmuxSession(ctx, f)
	if e != nil {
		return fmt.Errorf("summarize sessions: %w", e)
	}
	if app.outputJSON {
		return app.writeJSON(s)
	}
	return app.writeSummaryTable(s, o.full)
}

func (app *application) writeSummaryTable(ss []registry.Summary, full bool) error {
	const (
		summaryMuxWidth     = 10
		summarySessionWidth = 20
		summaryCountWidth   = 5
		summaryUnknownWidth = 6
	)
	labels := summaryTableLabels(ss)
	rows := make([][]string, 0, len(ss))
	for i, s := range ss {
		rows = append(rows, []string{multiplexerSummaryKind(s), labels[i], s.MultiplexerServerID, strconv.Itoa(s.Total), strconv.Itoa(s.Live), strconv.Itoa(s.Gone), strconv.Itoa(s.PresenceUnknown), strconv.Itoa(s.Running), strconv.Itoa(s.Waiting), strconv.Itoa(s.Idle), strconv.Itoa(s.Failed), strconv.Itoa(s.Interrupted), strconv.Itoa(s.ActivityUnknown)})
	}
	columns := []humanColumn{{heading: "MUX", width: summaryMuxWidth}, {heading: "Session", width: summarySessionWidth, wrap: wrapHumanSession}, {heading: "Server", width: summaryUnknownWidth, wrap: wrapHumanIdentifier}, {heading: "Total", width: summaryCountWidth, align: text.AlignRight}, {heading: "Live", width: summaryCountWidth, align: text.AlignRight}, {heading: "Gone", width: summaryCountWidth, align: text.AlignRight}, {heading: "Pres?", width: summaryUnknownWidth, align: text.AlignRight}, {heading: "Run", width: summaryCountWidth, align: text.AlignRight}, {heading: "Wait", width: summaryCountWidth, align: text.AlignRight}, {heading: "Idle", width: summaryCountWidth, align: text.AlignRight}, {heading: "Failed", width: summaryUnknownWidth, align: text.AlignRight}, {heading: "Interrupted", width: len("Interrupted"), align: text.AlignRight}, {heading: "Act?", width: summaryCountWidth, align: text.AlignRight}}
	for columnIndex := range columns {
		for _, row := range rows {
			columns[columnIndex].width = max(columns[columnIndex].width, text.StringWidth(row[columnIndex]))
		}
	}
	if validateHumanColumns(columns, app.maxLineWidth()) != nil {
		return app.writeStackedHumanRows(columns, rows)
	}
	if full {
		return app.writeWrappedHumanTable(columns, rows)
	}
	return app.writeHumanTable(columns, rows)
}

//nolint:gocritic // label precedence is intentionally explicit for stable output
func summaryTableLabels(ss []registry.Summary) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s.MultiplexerSessionName != "" {
			out = append(out, s.MultiplexerSessionName)
		} else if s.MultiplexerSessionID != "" {
			out = append(out, s.MultiplexerSessionID)
		} else if s.TmuxSessionName != "" {
			out = append(out, s.TmuxSessionName)
		} else if s.TmuxSessionID != "" {
			out = append(out, s.TmuxSessionID)
		} else {
			out = append(out, "unknown")
		}
	}
	return out
}

func (app *application) writeSessionDetails(session registry.Session) error {
	rows := []humanDetail{
		{label: "ID", value: session.ID},
		{label: "Agent", value: string(session.Harness)},
		{label: "Presence", value: string(session.Presence)},
		{label: "Activity", value: appReportActivity(session)},
		{label: "Session ID", value: session.SessionID},
		{label: "Session path", value: session.SessionPath},
		{label: "CWD", value: session.CWD},
		{label: "Project root", value: session.ProjectRoot},
		{label: "Resume command", value: strings.Join(session.ResumeCommand, " ")},
		{label: "Multiplexer", value: watchMultiplexerLabel(session.Multiplexer)},
		{label: "Tmux", value: watchTmuxLabel(session.Tmux)},
		{label: "Created", value: session.CreatedAt.Format(time.RFC3339)},
		{label: "Updated", value: session.UpdatedAt.Format(time.RFC3339)},
	}
	if session.Process != nil {
		rows = append(rows, humanDetail{label: "Process", value: fmt.Sprintf("pid=%d executable=%s", session.Process.PID, session.Process.Executable)})
	}
	return app.writeHumanDetails(rows)
}

func sortListSessions(ss []registry.Session, o listOptions) error {
	key := normalizeListSort(o.sortBy)
	cmp, e := listSortLess(key)
	if e != nil {
		return e
	}
	sort.SliceStable(ss, func(i, j int) bool {
		v := cmp(ss[i], ss[j])
		if o.desc {
			return v > 0
		}
		return v < 0
	})
	return nil
}

func listSortLess(k string) (sessionCompareFunc, error) {
	if c, ok := map[string]sessionCompareFunc{"multiplexer": compareSessionMultiplexer, "tmux": compareSessionTmux, "updated": compareSessionUpdated, "presence-changed": func(a, b registry.Session) int { return a.PresenceChangedAt.Compare(b.PresenceChangedAt) }, "activity-changed": func(a, b registry.Session) int { return a.ActivityChangedAt.Compare(b.ActivityChangedAt) }, "created": compareSessionCreated, "harness": func(a, b registry.Session) int { return strings.Compare(string(a.Harness), string(b.Harness)) }, "presence": func(a, b registry.Session) int { return strings.Compare(string(a.Presence), string(b.Presence)) }, "activity": func(a, b registry.Session) int { return strings.Compare(appReportActivity(a), appReportActivity(b)) }, "cwd": func(a, b registry.Session) int { return strings.Compare(a.CWD, b.CWD) }, "id": func(a, b registry.Session) int { return strings.Compare(a.ID, b.ID) }}[k]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("%w: %q", errInvalidListSort, k)
}

func normalizeListSort(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	if s == "" {
		return "updated"
	}
	return s
}

func compareSessionMultiplexer(a, b registry.Session) int {
	if comparison := strings.Compare(string(a.Multiplexer.Kind), string(b.Multiplexer.Kind)); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(a.Multiplexer.SessionName, b.Multiplexer.SessionName); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(multiplexerContainerLabel(a.Multiplexer), multiplexerContainerLabel(b.Multiplexer)); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(a.Multiplexer.PaneID, b.Multiplexer.PaneID); comparison != 0 {
		return comparison
	}
	return strings.Compare(a.ID, b.ID)
}

func compareSessionTmux(a, b registry.Session) int {
	if c := strings.Compare(a.Tmux.SessionName, b.Tmux.SessionName); c != 0 {
		return c
	}
	if c := strings.Compare(a.Tmux.WindowIndex, b.Tmux.WindowIndex); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func compareSessionUpdated(a, b registry.Session) int { return a.UpdatedAt.Compare(b.UpdatedAt) }

func compareSessionCreated(a, b registry.Session) int { return a.CreatedAt.Compare(b.CreatedAt) }

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

func readStdinPayloadData(stdin io.Reader, storeRaw, defaultsOnly bool) (json.RawMessage, json.RawMessage, error) {
	if !storeRaw && !defaultsOnly {
		return nil, nil, nil
	}
	var d []byte
	var err error
	if defaultsOnly {
		d, err = readPayloadDefaultsInput(stdin)
	} else {
		d, err = readPayloadInput(stdin)
	}
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

func sessionLabel(name, id string) string {
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "-"
}

func tmuxSessionLabel(c registry.TmuxContext) string {
	return sessionLabel(c.SessionName, c.SessionID)
}

func tmuxWindowLabel(c registry.TmuxContext) string {
	if c.WindowIndex != "" && c.WindowName != "" {
		return c.WindowIndex + ":" + c.WindowName
	}
	if c.WindowName != "" {
		return c.WindowName
	}
	if c.WindowIndex != "" {
		return c.WindowIndex
	}
	return "-"
}

func multiplexerSessionLabel(context registry.MultiplexerContext) string {
	return sessionLabel(context.SessionName, context.SessionID)
}

func multiplexerContainerLabel(context registry.MultiplexerContext) string {
	if context.Kind == registry.MultiplexerTmux {
		return tmuxWindowLabel(context.TmuxContext())
	}
	var parts []string
	if context.WorkspaceName != "" {
		parts = append(parts, context.WorkspaceName)
	} else if context.WorkspaceID != "" {
		parts = append(parts, context.WorkspaceID)
	}
	tab := context.TabName
	if tab == "" {
		tab = context.TabID
	}
	if tab == "" {
		tab = context.TabIndex
	}
	if tab != "" {
		parts = append(parts, tab)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "/")
}

func multiplexerSummaryKind(summary registry.Summary) string {
	if summary.MultiplexerKind != "" {
		return string(summary.MultiplexerKind)
	}
	if summary.TmuxSessionID != "" || summary.TmuxSessionName != "" {
		return string(registry.MultiplexerTmux)
	}
	return "unknown"
}

func formatUpdatedAt(t, now time.Time, absolute bool) string {
	if t.IsZero() {
		return "-"
	}
	if absolute {
		return t.Format(time.RFC3339)
	}
	d := now.Sub(t)
	if d < time.Second {
		return "just now"
	}
	return formatElapsed(d)
}

func formatHumanPath(p string) string {
	if p == "" {
		return ""
	}
	h, e := os.UserHomeDir()
	if e != nil {
		return p
	}
	r, e := filepath.Rel(h, p)
	if e != nil || filepath.IsAbs(r) || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return p
	}
	if r == "." {
		return "~"
	}
	return filepath.Join("~", r)
}

func formatElapsed(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < hoursPerDay*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(hoursPerDay*time.Hour)))
	case d < 365*hoursPerDay*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*hoursPerDay*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*hoursPerDay*time.Hour)))
	}
}
