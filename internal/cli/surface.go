package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	configSetArgsCount = 2
	configGetArgsCount = 1
)

var (
	errAllWithAgents       = errors.New("all cannot be combined with agent names")
	errAgentRequired       = errors.New("at least one agent or all is required")
	errCleanSelection      = errors.New("choose exactly one of --all or --older-than")
	errNegativeCleanAge    = errors.New("older-than must be nonnegative")
	errInfoReference       = errors.New("provide one session reference or --pane")
	errInfoConfig          = errors.New("--config-dir requires --explain")
	errStopSelection       = errors.New("provide one or more sessions, or --all")
	errStopAllConfirmation = errors.New(
		"stopping all sessions was not confirmed (pass -y to confirm)",
	)
	errCleanAllConfirmation = errors.New(
		"cleaning all gone sessions was not confirmed (pass -y to confirm)",
	)
	errIntegrationStatusFail    = errors.New("one or more integrations could not be inspected")
	errTargetBinaryNeedsShim    = errors.New("--target-binary requires --shim")
	errTargetBinaryWithAll      = errors.New("--target-binary cannot be used with all")
	errSetupHarnessRequired     = errors.New("setup requires at least one harness or 'all'")
	errInstallHarnessRequired   = errors.New("install requires at least one harness or 'all'")
	errRemoveHarnessRequired    = errors.New("remove requires at least one harness or 'all'")
	errNoConfigDisallowsInit    = errors.New("cannot initialize config when --no-config is set")
	errInitJSONTemplateConflict = errors.New("cannot output JSON when writing template to stdout")
	errNoConfigDisallowsSet     = errors.New("cannot set config when --no-config is set")
	errStdinDisallowsSet        = errors.New("cannot set key on stdin configuration")
	errUnknownConfigKey         = errors.New("unknown configuration key")
)

//exhaustruct:optional
type integrationCommandOptions struct {
	binary, targetBinary string
	dryRun, force, shim  bool
	showContent          bool
}
type setupResult struct {
	Integrations []install.Result `json:"integrations"`
	Tracker      service.Result   `json:"tracker"`
}

//exhaustruct:optional
type cleanOptions struct {
	all       bool
	olderThan time.Duration
	ageSet    bool
}

//exhaustruct:optional
type infoOptions struct {
	explain                 bool
	paneID                  string
	serverID                string
	multiplexerKind         string
	configDir               string
	disableScreenInspection bool
}

type explainedInfoResult struct {
	Session     registry.Session `json:"session"`
	Explanation explainResult    `json:"explanation"`
}

type configKeyOrigin struct {
	Source   string `json:"source"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	RawValue string `json:"raw_value,omitempty"`
}

type configProvenance struct {
	Config      config.Config              `json:"config"`
	ActiveFiles []string                   `json:"active_files"`
	Origins     map[string]configKeyOrigin `json:"origins"`
}

func (app *application) newSetupCommand() *cobra.Command {
	options := integrationCommandOptions{}
	serviceConfig := serviceOptions{interval: serviceDefaultInterval}

	var serviceOptions service.Options

	command := &cobra.Command{
		Use:           "setup <agent... | all>",
		Short:         "Set up harness integrations and start background tracking",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitCode(errSetupHarnessRequired, exitCodeUsage)
			}

			return nil
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if _, err := selectedHarnesses(args, false); err != nil {
				return exitCode(err, exitCodeUsage)
			}

			if options.binary == "" {
				options.binary = defaultInstallBinary()
			}

			serviceConfig.binary = options.binary
			serviceConfig.dryRun = options.dryRun

			var err error

			serviceOptions, err = app.configuredServiceOptions(cmd, serviceConfig)

			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			integrations, integrationErr := installIntegrations(cmd.Context(), args, options)

			tracker, trackerErr := runServiceOperation(cmd.Context(), "update", serviceOptions)
			if trackerErr != nil {
				trackerErr = fmt.Errorf("enable tracker: %w", trackerErr)
			}

			result := setupResult{Integrations: integrations, Tracker: tracker}
			if app.outputJSON {
				if err := app.writeJSON(result); err != nil {
					return err
				}

				return errors.Join(integrationErr, trackerErr)
			}

			if err := app.writeIntegrationResults(integrations, false); err != nil {
				return err
			}

			if err := app.writef("tracker: %s\nnext: aht list\n", tracker.Message); err != nil {
				return err
			}

			return errors.Join(integrationErr, trackerErr)
		},
	}
	flags := command.Flags()
	flags.StringVar(
		&options.binary,
		"binary",
		"",
		"AHT binary `<path>` used by integrations and tracker",
	)
	flags.BoolVarP(&options.dryRun, "dry-run", "n", false, "show changes without writing")
	flags.BoolVarP(&options.force, "force", "f", false, "replace foreign integration files")

	return command
}

func (app *application) newTrackerCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           trackerCommand,
		Short:         "Manage background session tracking",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	run := app.newTrackerRunCommand()
	run.Short = "Service entry point; not intended for manual use"
	command.AddCommand(
		run,
		app.newTrackerEnableCommand(),
		app.newTrackerDisableCommand(),
		app.newTrackerStatusCommand(),
	)

	return command
}

func (app *application) newTrackerEnableCommand() *cobra.Command {
	options := serviceOptions{interval: serviceDefaultInterval}

	var parsed service.Options

	command := &cobra.Command{
		Use:           "enable",
		Short:         "Install, update, and start background tracking",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error

			parsed, err = app.configuredServiceOptions(cmd, options)

			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := runServiceOperation(cmd.Context(), "update", parsed)
			if err != nil {
				return fmt.Errorf("enable tracker: %w", err)
			}

			return app.writeServiceResult(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.binary, "binary", "", "AHT binary `<path>` run by the tracker")
	flags.DurationVar(
		&options.interval,
		"interval",
		options.interval,
		"reconciliation `<duration>`",
	)
	flags.DurationVar(&options.grace, "grace-period", options.grace, "absence grace `<duration>`")
	flags.BoolVarP(&options.dryRun, "dry-run", "n", false, "show changes without writing")

	return command
}

func (app *application) newTrackerDisableCommand() *cobra.Command {
	dryRun := false
	command := &cobra.Command{
		Use:           "disable",
		Short:         "Stop and remove background tracking",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options, err := app.parseServiceOptions(
				serviceOptions{
					binary:   defaultInstallBinary(),
					interval: serviceDefaultInterval,
					dryRun:   dryRun,
				},
			)
			if err != nil {
				return err
			}

			result, err := runServiceOperation(cmd.Context(), "uninstall", options)
			if err != nil {
				return fmt.Errorf("disable tracker: %w", err)
			}

			return app.writeServiceResult(result)
		},
	}
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show changes without writing")

	return command
}

func (app *application) newTrackerStatusCommand() *cobra.Command {
	serviceConfig := serviceOptions{interval: serviceDefaultInterval}

	var options service.Options

	command := &cobra.Command{
		Use:           statusCommandName,
		Short:         "Show background tracking state",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error

			options, err = app.configuredServiceOptions(cmd, serviceConfig)

			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := runServiceOperation(cmd.Context(), statusCommandName, options)
			if err != nil {
				return fmt.Errorf("tracker status: %w", err)
			}

			return app.writeServiceResult(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&serviceConfig.binary, "binary", "", "expected AHT binary `<path>`")
	flags.DurationVar(
		&serviceConfig.interval,
		"interval",
		serviceConfig.interval,
		"expected reconciliation `<duration>`",
	)
	flags.DurationVar(
		&serviceConfig.grace,
		"grace-period",
		serviceConfig.grace,
		"expected absence grace `<duration>`",
	)

	return command
}

func (app *application) writeServiceResult(result service.Result) error {
	if app.outputJSON {
		return app.writeJSON(result)
	}

	state := "disabled"
	if result.Installed {
		state = "enabled"
	}

	return app.writeHumanDetails([]humanDetail{
		{label: "State", value: state},
		{label: "Manager", value: result.Manager},
		{label: "Path", value: result.ManagedPath},
		{label: "Version", value: strconv.Itoa(result.ManagedVersion)},
		{label: "Current", value: strconv.FormatBool(result.Current)},
		{label: "Running", value: strconv.FormatBool(result.Running)},
		{label: "Changed", value: strconv.FormatBool(result.Changed)},
		{label: "Message", value: result.Message},
	})
}

func (app *application) newStateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           stateCommandName,
		Short:         "Inspect or clean stored session state",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(
		app.newStatePathCommand(),
		app.newStateResetCommand(),
		app.newStateCleanCommand(),
	)

	return command
}

func (app *application) newStateCleanCommand() *cobra.Command {
	options := cleanOptions{}

	var yes bool

	command := &cobra.Command{
		Use:           "clean",
		Short:         "Delete gone session records",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			return app.prepareCleanOptions(cmd, &options)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runStateClean(cmd.Context(), options, yes)
		},
	}
	command.Flags().BoolVarP(&options.all, "all", "a", false, "delete every gone session record")
	command.Flags().
		BoolVarP(&yes, "yes", "y", false, "confirm deleting all gone records without prompting")
	command.Flags().
		DurationVar(&options.olderThan, "older-than", 0, "delete gone records older than this `<duration>`")

	return command
}

func (app *application) prepareCleanOptions(cmd *cobra.Command, options *cleanOptions) error {
	cfg, err := app.loadConfig()
	if err != nil {
		return err
	}

	options.ageSet = cmd.Flags().Changed("older-than")
	if options.all && options.ageSet {
		return exitCode(errCleanSelection, exitCodeUsage)
	}

	if !options.all && !options.ageSet && cfg.Retention.TombstoneTTL != "" {
		d, err := config.TombstoneTTL(cfg)
		if err != nil {
			return fmt.Errorf("parsing tombstone TTL: %w", err)
		}

		options.olderThan = d
		options.ageSet = true
	}

	if options.all == options.ageSet {
		return exitCode(errCleanSelection, exitCodeUsage)
	}

	if options.olderThan < 0 {
		return exitCode(errNegativeCleanAge, exitCodeUsage)
	}

	return nil
}

func (app *application) runStateClean(ctx context.Context, options cleanOptions, yes bool) error {
	if options.all && !yes {
		stdin := app.stdin
		if stdin == nil {
			stdin = os.Stdin
		}

		confirmed, err := app.confirmAction(
			ctx,
			stdin,
			"Delete all gone session records? [y/N]: ",
			"--yes",
		)
		if err != nil {
			return err
		}

		if !confirmed {
			return exitCode(errCleanAllConfirmation, exitCodeGeneral)
		}
	}

	return app.runRegistryClean(ctx, options)
}

func (app *application) runRegistryClean(ctx context.Context, opts cleanOptions) error {
	age := opts.olderThan
	if opts.all {
		age = 0
	}

	result, err := app.registryStore().GC(ctx, age)
	if err != nil {
		return fmt.Errorf("clean state: %w", err)
	}

	if app.outputJSON {
		return app.writeJSON(result)
	}

	return app.writef("deleted=%d remaining=%d\n", result.Deleted, result.Remaining)
}

func (app *application) newInfoCommand() *cobra.Command {
	options := infoOptions{}
	screenInspection := true
	command := &cobra.Command{
		Use:           "info [session]",
		Short:         "Show session details and optionally explain activity",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 0) == (options.paneID == "") {
				return exitCode(errInfoReference, exitCodeUsage)
			}

			if cmd.Flags().Changed("config-dir") && !options.explain {
				return exitCode(errInfoConfig, exitCodeUsage)
			}

			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("config-dir") && cfg.Detection.ManifestsDir != "" {
				options.configDir = cfg.Detection.ManifestsDir
			}

			options.disableScreenInspection = false
			if cfg.Detection.ScreenInspection != nil && !*cfg.Detection.ScreenInspection {
				options.disableScreenInspection = true
			}

			if cmd.Flags().Changed("screen-inspection") {
				options.disableScreenInspection = !screenInspection
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				session registry.Session
				err     error
			)
			if options.paneID != "" {
				session, err = app.resolvePaneSession(
					cmd.Context(),
					options.paneID,
					options.serverID,
					options.multiplexerKind,
				)
			} else {
				session, err = app.resolveSession(cmd.Context(), args[0])
			}

			if err != nil {
				return err
			}

			return app.writeInfo(cmd.Context(), session, options)
		},
	}
	command.Flags().
		BoolVar(&options.explain, "explain", false, "explain how the activity state was selected")
	command.Flags().StringVar(&options.paneID, "pane", "", "multiplexer pane `<id>`")
	command.Flags().StringVar(&options.serverID, "server", "", "multiplexer server `<id>`")
	command.Flags().
		StringVar(&options.multiplexerKind, "multiplexer", "", "multiplexer `<kind>`: tmux, zellij, herdr")
	command.Flags().
		StringVar(&options.configDir, "config-dir", "", "detection manifest override `<dir>`")
	command.Flags().
		BoolVar(&screenInspection, "screen-inspection", true, "enable terminal multiplexer screen inspection")

	return command
}

func (app *application) writeInfo(
	ctx context.Context,
	session registry.Session,
	opts infoOptions,
) error {
	if !opts.explain {
		if app.outputJSON {
			return app.writeJSON(session)
		}

		return app.writeSessionDetails(session)
	}

	explanation, explanationErr := evaluateExplanation(ctx, session, opts)
	if app.outputJSON {
		if err := app.writeJSON(
			explainedInfoResult{Session: session, Explanation: explanation},
		); err != nil {
			return err
		}

		return explanationErr
	}

	if err := app.writeSessionDetails(session); err != nil {
		return err
	}

	if err := app.writef("\nActivity diagnosis:\n"); err != nil {
		return err
	}

	if err := app.writeExplanationDetails(explanation); err != nil {
		return err
	}

	return explanationErr
}

func (app *application) resolveSession(
	ctx context.Context,
	reference string,
) (registry.Session, error) {
	session, err := app.registryStore().Resolve(ctx, client.Selector{
		ID:                "",
		Reference:         reference,
		Harness:           "",
		MultiplexerKind:   "",
		MultiplexerServer: "",
		MultiplexerPane:   "",
		Project:           "",
		ProjectSubtree:    false,
		CWD:               "",
	})
	if err != nil {
		return registry.Session{}, fmt.Errorf("resolve session: %w", err)
	}

	return session, nil
}

func (app *application) newWatchCommand() *cobra.Command {
	options := listOptions{}

	var (
		noSnapshot  bool
		watchFormat string
		prepared    watchOptions
	)

	command := &cobra.Command{
		Use:           "watch",
		Short:         "Stream session changes",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("presence") && cfg.UI.DefaultPresence != "" {
				options.presence = cfg.UI.DefaultPresence
			}

			filter, err := buildFilter(options)
			if err != nil {
				return err
			}

			prepared, err = app.prepareWatch(watchOptions{
				filter:     filter,
				harness:    options.harness,
				noSnapshot: noSnapshot,
				format:     watchFormat,
				formatSet:  cmd.Flags().Changed("format"),
			})

			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runWatch(cmd.Context(), prepared)
		},
	}
	flags := command.Flags()
	configureSessionFilterFlags(flags, &options)
	flags.BoolVar(&noSnapshot, "no-snapshot", false, "start with future changes only")
	flags.StringVar(&watchFormat, "format", "", "output format: `<table|plain>`")

	return command
}

func (app *application) newStopCommand() *cobra.Command {
	all := false
	dryRun := false
	yes := false
	command := &cobra.Command{
		Use:           "stop [session...]",
		Short:         "Gracefully stop sessions",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return exitCode(errStopSelection, exitCodeUsage)
			}

			if !all && len(args) == 0 {
				return exitCode(errStopSelection, exitCodeUsage)
			}

			if all && !yes && !dryRun {
				stdin := app.stdin
				if stdin == nil {
					stdin = os.Stdin
				}

				confirmed, err := app.confirmStopAll(cmd.Context(), stdin)
				if err != nil {
					return err
				}

				if !confirmed {
					return exitCode(errStopAllConfirmation, exitCodeGeneral)
				}
			}

			return app.runStop(cmd.Context(), args, all, dryRun)
		},
	}
	command.Flags().BoolVarP(&all, "all", "a", false, "stop every live session")
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show targets without sending signals")
	command.Flags().
		BoolVarP(&yes, "yes", "y", false, "confirm stopping all sessions without prompting")

	return command
}

func (app *application) newManageConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "config",
		Short:         "Inspect configuration path and effective settings",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(
		app.newConfigPathCommand(),
		app.newConfigShowCommand(),
		app.newConfigGetCommand(),
		app.newConfigSetCommand(),
		app.newConfigInitCommand(),
		app.newConfigSchemaCommand(),
	)

	return command
}

func (app *application) newConfigPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "path",
		Short:         "Print resolved configuration file path",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			path := app.resolvedConfigPath
			if path == "" {
				path = config.DefaultPath()
			}

			if app.outputJSON {
				return app.writeJSON(map[string]string{"path": path})
			}

			return app.writef("%s\n", path)
		},
	}
}

func (app *application) newConfigShowCommand() *cobra.Command {
	var showProvenance bool

	cmd := &cobra.Command{
		Use:           "show",
		Short:         "Print effective configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			return err
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runConfigShow(showProvenance)
		},
	}
	cmd.Flags().
		BoolVar(&showProvenance, "provenance", false, "include layer provenance for resolved keys")

	return cmd
}

func (app *application) runConfigShow(showProvenance bool) error {
	if app.outputJSON {
		if showProvenance && app.cfgMeta != nil {
			return app.writeJSON(app.configWithProvenance())
		}

		return app.writeJSON(app.cfg)
	}

	var b strings.Builder

	if showProvenance && app.cfgMeta != nil {
		app.appendConfigProvenance(&b)
	}

	data, err := toml.Marshal(app.cfg)
	if err != nil {
		return fmt.Errorf("encode config toml: %w", err)
	}

	b.Write(data)

	return app.writef("%s", b.String())
}

func (app *application) configWithProvenance() configProvenance {
	origins := make(map[string]configKeyOrigin)

	for _, key := range configKeys() {
		if origin, ok := app.cfgMeta.Where(key); ok {
			origins[key] = configKeyOrigin{
				Source:   string(origin.Source),
				Path:     origin.Path,
				Line:     origin.Line,
				RawValue: origin.RawValue,
			}
		}
	}

	return configProvenance{
		Config:      app.cfg,
		ActiveFiles: app.cfgMeta.ActiveFiles(),
		Origins:     origins,
	}
}

func (app *application) appendConfigProvenance(b *strings.Builder) {
	active := app.cfgMeta.ActiveFiles()
	if len(active) > 0 {
		fmt.Fprintf(b, "# Active configuration files (highest precedence last):\n")

		for _, file := range active {
			fmt.Fprintf(b, "#   - %s\n", file)
		}

		fmt.Fprintf(b, "\n")
	}

	for _, key := range configKeys() {
		if origin, ok := app.cfgMeta.Where(key); ok {
			fmt.Fprintf(b, "# %s (from %s)\n", key, configOriginLabel(origin))
		}
	}

	fmt.Fprintf(b, "\n")
}

func (app *application) newConfigGetCommand() *cobra.Command {
	var showProvenance bool

	cmd := &cobra.Command{
		Use:           "get <key>",
		Short:         "Get a configuration setting by key",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(configGetArgsCount),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			return err
		},
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runConfigGet(args[0], showProvenance)
		},
	}
	cmd.Flags().
		BoolVar(&showProvenance, "provenance", false, "include layer provenance for the key")

	return cmd
}

func (app *application) runConfigGet(key string, showProvenance bool) error {
	val, ok := getConfigValue(app.cfg, key)
	if !ok {
		return exitCode(fmt.Errorf("%w: %q", errUnknownConfigKey, key), exitCodeUsage)
	}

	if app.outputJSON {
		return app.writeConfigValueJSON(key, val, showProvenance)
	}

	if showProvenance && app.cfgMeta != nil {
		if origin, found := app.cfgMeta.Where(key); found {
			return app.writef("%v (from %s)\n", val, configOriginLabel(origin))
		}
	}

	return app.writef("%v\n", val)
}

func (app *application) writeConfigValueJSON(key string, val any, showProvenance bool) error {
	result := map[string]any{"key": key, "value": val}
	if showProvenance && app.cfgMeta != nil {
		if origin, found := app.cfgMeta.Where(key); found {
			result["origin"] = map[string]any{
				"source": string(origin.Source), "path": origin.Path,
				"line": origin.Line, "raw_value": origin.RawValue,
			}
		}
	}

	return app.writeJSON(result)
}

func getConfigValue(cfg config.Config, key string) (any, bool) {
	values := map[string]any{
		"ui.default_presence":         cfg.UI.DefaultPresence,
		"ui.sort":                     cfg.UI.Sort,
		"ui.sort_desc":                configBool(cfg.UI.SortDesc, false),
		"ui.absolute_time":            configBool(cfg.UI.AbsoluteTime, false),
		"ui.time_format":              cfg.UI.TimeFormat,
		"retention.tombstone_ttl":     cfg.Retention.TombstoneTTL,
		"filter.ignore_harnesses":     cfg.Filter.IgnoreHarnesses,
		"filter.ignore_paths":         cfg.Filter.IgnorePaths,
		"tracker.interval":            cfg.Tracker.Interval,
		"tracker.grace_period":        cfg.Tracker.GracePeriod,
		"tracker.quiet":               configBool(cfg.Tracker.Quiet, false),
		"detection.manifests_dir":     cfg.Detection.ManifestsDir,
		"detection.screen_inspection": configBool(cfg.Detection.ScreenInspection, true),
	}
	value, ok := values[strings.ToLower(strings.TrimSpace(key))]

	return value, ok
}

func configBool(value *bool, fallback bool) bool {
	if value != nil {
		return *value
	}

	return fallback
}

func configKeys() []string {
	return []string{
		"ui.default_presence", "ui.sort", "ui.sort_desc", "ui.absolute_time", "ui.time_format",
		"retention.tombstone_ttl", "filter.ignore_harnesses", "filter.ignore_paths",
		"tracker.interval", "tracker.grace_period", "tracker.quiet",
		"detection.manifests_dir", "detection.screen_inspection",
	}
}

func configOriginLabel(origin strata.Origin) string {
	switch {
	case origin.Line > 0:
		return fmt.Sprintf("%s:%d", origin.Path, origin.Line)
	case origin.Path != "":
		return fmt.Sprintf("%s %s", origin.Source, origin.Path)
	default:
		return string(origin.Source)
	}
}

func (app *application) newConfigSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "schema",
		Short:         "Output JSON Schema for AHT configuration",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.writef("%s", config.Schema)
		},
	}
}

func (app *application) newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "set <key> <value>",
		Short:         "Set a configuration key in the active config file",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(configSetArgsCount),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if app.noConfig {
				return exitCode(errNoConfigDisallowsSet, exitCodeUsage)
			}

			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			key, val := args[0], args[1]

			path, err := app.configEditPath()
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}

			if err := setConfigValue(path, key, val); err != nil {
				return err
			}

			if app.outputJSON {
				return app.writeJSON(map[string]any{
					"path":  path,
					"key":   key,
					"value": val,
					"set":   true,
				})
			}

			return app.writef("set %s = %s in %s\n", key, val, path)
		},
	}
}

func (app *application) configEditPath() (string, error) {
	if app.configPath == "-" {
		return "", errStdinDisallowsSet
	}

	options := []strata.Option{strata.WithFormats(".toml")}
	if app.configPath != "" {
		options = append(options, strata.WithPath(app.configPath))
	} else if path := strings.TrimSpace(os.Getenv(config.ConfigEnv)); path != "" {
		options = append(options, strata.WithOptionalPath(path))
	} else {
		options = append(options, strata.WithAppName("aht"))
	}

	path, err := strata.EditPath(options...)
	if err != nil {
		return "", fmt.Errorf("select config edit path: %w", err)
	}

	return path, nil
}

func setConfigValue(path string, key string, value string) error {
	if err := ensureEditableConfigFile(path); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	updated, err := strata.SetBytes(".toml", data, key, value)
	if err != nil {
		return exitCode(fmt.Errorf("set key %q: %w", key, err), exitCodeUsage)
	}

	_, err = strata.Load[config.Config](
		strata.WithPath("-"),
		strata.WithStdin(bytes.NewReader(updated)),
		strata.WithFormats(".toml"),
		strata.WithStrict(),
	)
	if err != nil {
		return exitCode(fmt.Errorf("updated configuration is invalid: %w", err), exitCodeUsage)
	}

	if err := strata.Set[config.Config](path, key, value); err != nil {
		return exitCode(fmt.Errorf("set key %q: %w", key, err), exitCodeUsage)
	}

	return nil
}

func ensureEditableConfigFile(path string) error {
	if _, err := config.EnsureConfigFile(path); err != nil {
		return fmt.Errorf("ensure config file %s: %w", path, err)
	}

	return nil
}

func (app *application) newConfigInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Initialize default configuration file",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runConfigInit(force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing configuration file")

	return cmd
}

func (app *application) runConfigInit(force bool) error {
	if app.noConfig {
		return exitCode(errNoConfigDisallowsInit, exitCodeUsage)
	}

	if app.configPath == "-" && app.outputJSON {
		return exitCode(errInitJSONTemplateConflict, exitCodeUsage)
	}

	if app.configPath == "-" {
		return app.writef("%s", config.DefaultConfigTemplate())
	}

	path, err := app.configEditPath()
	if err != nil {
		return exitCode(err, exitCodeUsage)
	}

	exists, err := configInitFileExists(path)
	if err != nil {
		return err
	}

	if exists && !force {
		return app.reportConfigInit(path, false)
	}

	if err := config.WriteConfigFile(path); err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	return app.reportConfigInit(path, true)
}

func configInitFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w %s: %w", config.ErrAccessConfig, path, err)
	}

	if err == nil && info.IsDir() {
		return false, fmt.Errorf("%w: %s", config.ErrConfigIsDirectory, path)
	}

	return err == nil, nil
}

func (app *application) reportConfigInit(path string, created bool) error {
	if !created {
		if app.outputJSON {
			return app.writeJSON(map[string]any{
				"created": false, "path": path,
				"message": "config file already exists (use --force to overwrite)",
			})
		}

		app.warnf("config file already exists at %s (use --force to overwrite)\n", path)

		return nil
	}

	if app.outputJSON {
		return app.writeJSON(map[string]any{"created": true, "path": path})
	}

	app.warnf("created %s\n", path)

	return nil
}
