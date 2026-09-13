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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/urfave/cli/v3"

	urfavehelp "github.com/zigai/urfave-help"

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
	exitCodeGeneral                  = 1
	exitCodeUsage                    = 2
	exitCodeInterrupted              = 130
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
	errUnexpectedArgument        = errors.New("unexpected argument")
	errUnknownCommand            = errors.New("unknown command")
	setupCLIFlagsOnce            sync.Once
)

type application struct {
	storePath          string
	configPath         string
	configExplicit     bool
	noConfig           bool
	cfgLoaded          bool
	cfg                config.Config
	resolvedConfigPath string
	cfgErr             error
	outputJSON         bool
	stdin              io.Reader
	stdout             io.Writer
	stderr             io.Writer
}

type exitCoderError struct {
	err  error
	code int
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

func (e *exitCoderError) Error() string {
	return e.err.Error()
}

func (e *exitCoderError) Unwrap() error {
	return e.err
}

func (e *exitCoderError) ExitCode() int {
	return e.code
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := executeCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	if code != 0 {
		os.Exit(code)
	}
}

func NewRootCommand(stdout io.Writer, stderr io.Writer) *cli.Command {
	setupGlobalCLIDefaults()
	return (&application{stdout: stdout, stderr: stderr}).newRootCommand()
}

func setupGlobalCLIDefaults() {
	setupCLIFlagsOnce.Do(func() {
		cli.VersionFlag = &cli.BoolFlag{
			Name:    "version",
			Aliases: []string{"V"},
			Usage:   "print version",
		}
		cli.HelpFlag = &cli.BoolFlag{
			Name:  "help",
			Usage: "show help",
		}
		urfavehelp.Install()
		cli.VersionPrinter = func(cmd *cli.Command) {
			root := cmd.Root()
			if root.Bool("json") {
				data, err := json.MarshalIndent(map[string]string{
					"version": version,
					"commit":  commit,
					"built":   date,
				}, "", jsonIndent)
				if err != nil {
					return
				}
				_, _ = fmt.Fprintln(root.Writer, string(data))
				return
			}
			_, _ = fmt.Fprintf(root.Writer, "aht %s (commit: %s, built: %s)\n", version, commit, date)
		}
	})
}

func exitCode(err error, code int) error {
	if err == nil {
		return nil
	}
	return &exitCoderError{err: err, code: code}
}

func unexpectedArgsError(args []string) error {
	return exitCode(fmt.Errorf("%w: %s", errUnexpectedArgument, strings.Join(args, " ")), exitCodeUsage)
}

func (app *application) loadConfig() (config.Config, error) {
	if app.cfgLoaded {
		return app.cfg, app.cfgErr
	}
	app.configExplicit = app.configPath != ""
	targetPath := app.configPath
	if targetPath == "" {
		targetPath = config.DefaultPath()
	}
	if !app.configExplicit && !app.noConfig && targetPath != "-" {
		if _, statErr := os.Stat(targetPath); errors.Is(statErr, os.ErrNotExist) {
			_, _ = config.EnsureConfigFile(targetPath)
		}
	}
	cfg, resolved, err := config.LoadWithOptions(config.Options{
		Path:     app.configPath,
		Explicit: app.configExplicit,
		NoConfig: app.noConfig,
		Stdin:    app.stdin,
	})
	app.cfg = cfg
	app.resolvedConfigPath = resolved
	app.cfgErr = err
	app.cfgLoaded = true
	return app.cfg, app.cfgErr
}

func (app *application) newRootCommand() *cli.Command {
	setupGlobalCLIDefaults()
	root := &cli.Command{
		Name:            "aht",
		Usage:           "Track local coding-agent sessions and where they are running",
		Version:         version,
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "store",
				Destination: &app.storePath,
				Usage:       "Registry state `path`",
			},
			&cli.StringFlag{
				Name:        "config",
				Destination: &app.configPath,
				Usage:       "Config file `path`",
			},
			&cli.BoolFlag{
				Name:        "no-config",
				Destination: &app.noConfig,
				Usage:       "bypass all configuration files",
			},
			&cli.BoolFlag{
				Name:        "json",
				Destination: &app.outputJSON,
				Usage:       "emit JSON (JSON Lines for streams)",
			},
		},
		Commands: []*cli.Command{
			app.newListCommand(),
			app.newWatchCommand(),
			app.newInfoCommand(),
			app.newStopCommand(),
			app.newManageCommand(),
			app.newHookCommand(),
			app.newReportCommand(),
		},
		EnableShellCompletion: true,
		ConfigureShellCompletionCommand: func(cmd *cli.Command) {
			cmd.Hidden = false
			cmd.Metadata = map[string]any{
				helpArgumentsKey: []HelpArg{
					{Name: "<shell>", Desc: "Shell type: bash, zsh, fish, or powershell"},
				},
			}
			for _, sub := range cmd.Commands {
				if sub.Name == "pwsh" {
					sub.Name = "powershell"
					sub.Usage = "Output powershell completion script"
				}
			}
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			app.configExplicit = cmd.IsSet("config")
			return ctx, nil
		},
		OnUsageError: func(ctx context.Context, cmd *cli.Command, err error, isSubcommand bool) error {
			return exitCode(err, exitCodeUsage)
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return exitCode(fmt.Errorf("%w %q for %q", errUnknownCommand, cmd.Args().First(), cmd.Name), exitCodeUsage)
			}
			return cli.ShowAppHelp(cmd)
		},
		ExitErrHandler: func(ctx context.Context, cmd *cli.Command, err error) {
			// Return error to executeCLI without printing or exiting
		},
	}
	_ = root.Walk(func(sub *cli.Command) error {
		if sub.OnUsageError == nil {
			sub.OnUsageError = func(ctx context.Context, cmd *cli.Command, err error, isSubcommand bool) error {
				return exitCode(err, exitCodeUsage)
			}
		}
		return nil
	})
	root.Reader = app.stdin
	root.Writer = app.stdout
	root.ErrWriter = app.stderr
	return root
}

func executeCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	app := &application{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	root := app.newRootCommand()
	osArgs := append([]string{"aht"}, args...)
	err := root.Run(ctx, osArgs)
	if err != nil {
		return app.handleError(err)
	}
	return 0
}

func (app *application) handleError(err error) int {
	if errors.Is(err, syscall.EPIPE) {
		return sigpipeExitCode
	}
	if exitCoder, ok := errors.AsType[cli.ExitCoder](err); ok {
		code := exitCoder.ExitCode()
		if code == sigpipeExitCode {
			return sigpipeExitCode
		}
		if errors.Is(err, context.Canceled) || code == exitCodeInterrupted {
			return exitCodeInterrupted
		}
		if err.Error() != "" {
			_, _ = fmt.Fprintln(app.stderr, err.Error())
		}
		return code
	}
	if errors.Is(err, context.Canceled) {
		return exitCodeInterrupted
	}
	_, _ = fmt.Fprintln(app.stderr, err.Error())
	return exitCodeGeneral
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

func (app *application) newRegistryPathCommand() *cli.Command {
	return &cli.Command{
		Name:  "path",
		Usage: "Print the registry state file path",
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			if app.outputJSON {
				return app.writeJSON(map[string]string{"path": app.resolvedStorePath()})
			}
			return app.writeln(app.resolvedStorePath())
		},
	}
}

func (app *application) newReportCommand() *cli.Command {
	options := defaultReportOptionsFromEnv()
	return &cli.Command{
		Name:                      "report",
		Usage:                     "Record a harness observation",
		ArgsUsage:                 "[harness]",
		Hidden:                    true,
		Description:               "Record a harness observation",
		DisableSliceFlagSeparator: true,
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "[harness]", Desc: "Target harness to record observation for"},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "presence", Value: options.presence, Destination: &options.presence, Usage: "presence: live, gone, unknown"},
			&cli.StringFlag{Name: "activity", Value: options.activity, Destination: &options.activity, Usage: "reported activity hint: running, waiting, idle, unknown"},
			&cli.StringFlag{Name: "lifecycle", Value: options.lifecycle, Destination: &options.lifecycle, Usage: "native lifecycle: start, resume, end", Hidden: true},
			&cli.StringFlag{Name: "session-id", Value: options.sessionID, Destination: &options.sessionID, Usage: "harness session id"},
			&cli.StringFlag{Name: "session-path", Value: options.sessionPath, Destination: &options.sessionPath, Usage: "harness session file path"},
			&cli.StringFlag{Name: "cwd", Value: options.cwd, Destination: &options.cwd, Usage: "agent current working directory"},
			&cli.StringFlag{Name: "project-root", Value: options.projectRoot, Destination: &options.projectRoot, Usage: "project root"},
			&cli.IntFlag{Name: "pid", Value: options.pid, Destination: &options.pid, Usage: "agent process id"},
			&cli.IntFlag{Name: "ppid", Value: options.ppid, Destination: &options.ppid, Usage: "agent parent process id"},
			&cli.IntFlag{Name: "process-group-id", Value: options.processGroupID, Destination: &options.processGroupID, Usage: "agent process group id"},
			&cli.StringFlag{Name: "start-identity", Value: options.startIdentity, Destination: &options.startIdentity, Usage: "process start identity"},
			&cli.StringFlag{Name: "executable", Value: options.executable, Destination: &options.executable, Usage: "resolved executable path"},
			&cli.StringFlag{Name: "tty", Value: options.tty, Destination: &options.tty, Usage: "agent tty"},
			&cli.StringFlag{Name: "event", Value: options.event, Destination: &options.event, Usage: "native harness event name"},
			&cli.StringFlag{Name: "observed-at", Value: options.observedAt, Destination: &options.observedAt, Usage: "RFC3339 timestamp"},
			&cli.StringFlag{Name: "sequence", Value: options.sequence, Destination: &options.sequence, Usage: "strictly increasing integration report sequence"},
			&cli.StringSliceFlag{Name: "attribute", Destination: &options.attributes, Usage: "extra key=value attribute"},
			&cli.StringSliceFlag{Name: "resume-command", Destination: &options.resumeCommand, Usage: "resume command argv item, repeatable"},
			&cli.StringFlag{Name: "evidence", Value: options.evidence, Destination: &options.evidence, Usage: "evidence kind (managed shims)", Hidden: true},
			&cli.BoolFlag{Name: "raw-stdin", Destination: &options.rawStdin, Usage: "store stdin as raw hook payload"},
			&cli.BoolFlag{Name: "raw-stdin-defaults-only", Destination: &options.rawDefaultsOnly, Usage: "read stdin for defaults without storing raw payload"},
			&cli.BoolFlag{Name: "no-tmux", Destination: &options.noTmux, Usage: "do not collect tmux context"},
			&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Destination: &options.quiet, Usage: "suppress human-readable output"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) > 1 {
				return unexpectedArgsError(args[1:])
			}
			if len(args) == 1 {
				if options.harness != "" {
					return fmt.Errorf("%w: harness already set", errUnexpectedReportArg)
				}
				options.harness = args[0]
			}
			if cmd.IsSet("cwd") {
				options.cwdAuto = false
			}
			if cmd.IsSet("project-root") {
				options.projectRootAuto = false
			}
			stdin := app.stdin
			if stdin == nil {
				stdin = os.Stdin
			}
			return app.runReport(ctx, stdin, options)
		},
	}
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

func (app *application) newListCommand() *cli.Command {
	o := listOptions{}
	return &cli.Command{
		Name:     listCommandName,
		Usage:    "Show known sessions",
		Category: "Sessions",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "agent", Destination: &o.harness, Usage: "Filter by agent `name`"},
			&cli.StringFlag{Name: "presence", Destination: &o.presence, Usage: "Filter by presence (live, gone, unknown, all)"},
			&cli.StringFlag{Name: "activity", Destination: &o.activity, Usage: "Filter by reported activity (running, waiting, idle, unknown)"},
			&cli.StringFlag{Name: "tmux-session", Destination: &o.tmuxSession, Usage: "Filter by tmux session `name`"},
			&cli.StringFlag{Name: "multiplexer-session", Destination: &o.multiplexerSession, Usage: "Filter by multiplexer session `name`"},
			&cli.StringFlag{Name: "sort", Destination: &o.sortBy, Usage: "Sort by: updated, created, harness, presence, activity, cwd, id, multiplexer, tmux, presence-changed, activity-changed"},
			&cli.BoolFlag{Name: "summary", Destination: &o.summary, Usage: "summarize agent counts by multiplexer session"},
			&cli.BoolFlag{Name: "absolute-time", Destination: &o.absoluteTime, Usage: "show full timestamps"},
			&cli.BoolFlag{Name: "desc", Destination: &o.desc, Usage: "sort descending"},
			&cli.BoolFlag{Name: "full", Destination: &o.full, Usage: "show complete values using an adaptive layout"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			applyListConfig(&o, cmd, cfg)
			return app.runList(ctx, o)
		},
	}
}

func applyListConfig(o *listOptions, cmd *cli.Command, cfg config.Config) {
	if !cmd.IsSet("presence") && cfg.UI.DefaultPresence != "" {
		o.presence = cfg.UI.DefaultPresence
	}
	o.sortSet = cmd.IsSet("sort")
	if !o.sortSet && cfg.UI.Sort != "" {
		o.sortBy = cfg.UI.Sort
	}
	o.descSet = cmd.IsSet("desc")
	if !o.descSet && cfg.UI.SortDesc != nil {
		o.desc = *cfg.UI.SortDesc
	}
	o.absoluteSet = cmd.IsSet("absolute-time")
	if !o.absoluteSet {
		if (cfg.UI.AbsoluteTime != nil && *cfg.UI.AbsoluteTime) ||
			cfg.UI.TimeFormat == "absolute" || cfg.UI.TimeFormat == "iso8601" {
			o.absoluteTime = true
		}
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

//nolint:gocognit // prefix grouping and neighbor common prefix calculation
func abbreviatedRegistryIDs(sessions []registry.Session) map[string]string {
	if len(sessions) == 0 {
		return nil
	}
	type item struct {
		id     string
		suffix string
	}
	byPrefix := make(map[string][]item)
	for _, session := range sessions {
		p, s := splitRegistryID(session.ID)
		byPrefix[p] = append(byPrefix[p], item{id: session.ID, suffix: s})
	}

	result := make(map[string]string, len(sessions))
	for prefix, items := range byPrefix {
		if len(items) == 1 {
			it := items[0]
			if len(it.suffix) <= registryIDShortLength {
				result[it.id] = it.id
			} else {
				result[it.id] = prefix + it.suffix[:registryIDShortLength]
			}
			continue
		}
		slices.SortFunc(items, func(a, b item) int {
			return strings.Compare(a.suffix, b.suffix)
		})
		for i, it := range items {
			if len(it.suffix) <= registryIDShortLength {
				result[it.id] = it.id
				continue
			}
			length := registryIDShortLength
			if i > 0 {
				common := commonPrefixLength(it.suffix, items[i-1].suffix)
				if common >= length {
					length = min(common+1, len(it.suffix))
				}
			}
			if i+1 < len(items) {
				common := commonPrefixLength(it.suffix, items[i+1].suffix)
				if common >= length {
					length = min(common+1, len(it.suffix))
				}
			}
			result[it.id] = prefix + it.suffix[:length]
		}
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
