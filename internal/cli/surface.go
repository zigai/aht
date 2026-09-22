package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/config"
	harnesspkg "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

var (
	errAllWithAgents            = errors.New("all cannot be combined with agent names")
	errAgentRequired            = errors.New("at least one agent or all is required")
	errCleanSelection           = errors.New("choose exactly one of --all or --older-than")
	errNegativeCleanAge         = errors.New("older-than must be nonnegative")
	errInfoReference            = errors.New("provide one session reference or --pane")
	errInfoConfig               = errors.New("--config-dir requires --explain")
	errStopSelection            = errors.New("provide one or more sessions, or --all")
	errStopAllConfirmation      = errors.New("stopping all sessions was not confirmed (pass -y to confirm)")
	errCleanAllConfirmation     = errors.New("cleaning all gone sessions was not confirmed (pass -y to confirm)")
	errIntegrationStatusFail    = errors.New("one or more integrations could not be inspected")
	errTargetBinaryNeedsShim    = errors.New("--target-binary requires --shim")
	errTargetBinaryWithAll      = errors.New("--target-binary cannot be used with all")
	errSetupHarnessRequired     = errors.New("setup requires at least one harness or 'all'")
	errInstallHarnessRequired   = errors.New("install requires at least one harness or 'all'")
	errRemoveHarnessRequired    = errors.New("remove requires at least one harness or 'all'")
	errNoConfigDisallowsInit    = errors.New("cannot initialize config when --no-config is set")
	errInitJSONTemplateConflict = errors.New("cannot output JSON when writing template to stdout")
)

type integrationCommandOptions struct {
	binary, targetBinary string
	dryRun, force, shim  bool
	showContent          bool
}

type setupResult struct {
	Integrations []install.Result `json:"integrations"`
	Tracker      service.Result   `json:"tracker"`
}

type cleanOptions struct {
	all       bool
	olderThan time.Duration
	ageSet    bool
}

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
	flags.StringVar(&options.binary, "binary", "", "AHT binary `<path>` used by integrations and tracker")
	flags.BoolVarP(&options.dryRun, "dry-run", "n", false, "show changes without writing")
	flags.BoolVarP(&options.force, "force", "f", false, "replace foreign integration files")
	return command
}

func (app *application) newIntegrationsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           integrationsCommand,
		Short:         "Install, remove, and inspect agent integrations",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = cmd.Help()
			return exitCode(errSilentUsageError, exitCodeUsage)
		},
	}
	command.AddCommand(
		app.newIntegrationsInstallCommand(),
		app.newIntegrationsRemoveCommand(),
		app.newIntegrationsStatusCommand(),
	)
	return command
}

func (app *application) newIntegrationsInstallCommand() *cobra.Command {
	options := integrationCommandOptions{}
	command := &cobra.Command{
		Use:           installCommandName + " <agent... | all>",
		Short:         "Install or update agent integrations",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitCode(errInstallHarnessRequired, exitCodeUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("target-binary") && !options.shim {
				return exitCode(errTargetBinaryNeedsShim, exitCodeUsage)
			}
			if cmd.Flags().Changed("target-binary") && len(args) == 1 && strings.EqualFold(args[0], "all") {
				return exitCode(errTargetBinaryWithAll, exitCodeUsage)
			}
			if options.binary == "" {
				options.binary = defaultInstallBinary()
			}
			results, err := installIntegrations(cmd.Context(), args, options)
			if app.outputJSON {
				if writeErr := app.writeJSON(results); writeErr != nil {
					return writeErr
				}
			} else if writeErr := app.writeIntegrationResults(results, options.showContent); writeErr != nil {
				return writeErr
			}
			return err
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.binary, "binary", "", "AHT binary `<path>` used by installed integrations")
	flags.StringVar(&options.targetBinary, "target-binary", "", "real agent binary `<path>` for shim installs")
	flags.BoolVarP(&options.dryRun, "dry-run", "n", false, "show changes without writing")
	flags.BoolVarP(&options.force, "force", "f", false, "replace a foreign integration file")
	flags.BoolVar(&options.shim, "shim", false, "install the documented process-lifetime fallback")
	flags.BoolVar(&options.showContent, "show-content", false, "print generated integration content")
	return command
}

func (app *application) newIntegrationsRemoveCommand() *cobra.Command {
	options := integrationCommandOptions{}
	command := &cobra.Command{
		Use:           "remove <agent... | all>",
		Short:         "Remove aht-owned integrations",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return exitCode(errRemoveHarnessRequired, exitCodeUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			harnesses, err := selectedHarnesses(args, false)
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}
			results := make([]install.Result, 0, len(harnesses))
			var failures []error
			for _, harnessID := range harnesses {
				result, removeErr := install.RemoveContext(cmd.Context(), install.Options{Harness: harnessID, Binary: options.binary, DryRun: options.dryRun})
				if removeErr != nil {
					result = failedIntegrationResult(harnessID, "remove failed", removeErr)
					failures = append(failures, removeErr)
				}
				results = append(results, result)
			}
			if app.outputJSON {
				if writeErr := app.writeJSON(results); writeErr != nil {
					return writeErr
				}
			} else if writeErr := app.writeIntegrationResults(results, false); writeErr != nil {
				return writeErr
			}
			return errors.Join(failures...)
		},
	}
	command.Flags().BoolVarP(&options.dryRun, "dry-run", "n", false, "show changes without writing")
	return command
}

func (app *application) newIntegrationsStatusCommand() *cobra.Command {
	var binary string
	command := &cobra.Command{
		Use:           "status [agent...]",
		Short:         "Show integration installation state",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if binary == "" {
				binary = defaultInstallBinary()
			}
			return app.runIntegrationsStatus(cmd.Context(), args, binary)
		},
	}
	command.Flags().StringVar(&binary, "binary", "", "expected AHT binary `<path>`")
	return command
}

func (app *application) runIntegrationsStatus(ctx context.Context, args []string, binary string) error {
	harnesses, err := selectedHarnesses(args, true)
	if err != nil {
		return err
	}
	results, failed := inspectIntegrationStatuses(ctx, harnesses, binary)
	if err := app.writeIntegrationStatuses(results); err != nil {
		return err
	}
	if failed {
		return errIntegrationStatusFail
	}

	return nil
}

func inspectIntegrationStatuses(ctx context.Context, harnesses []registry.Harness, binary string) ([]install.IntegrationStatus, bool) {
	results := make([]install.IntegrationStatus, 0, len(harnesses))
	failed := false
	for _, harnessID := range harnesses {
		status, err := install.InspectContext(ctx, harnessID, binary)
		if err != nil {
			failed = true
			status = install.IntegrationStatus{
				Harness:  harnessID,
				Status:   install.ArtifactForeign,
				Paths:    nil,
				Message:  err.Error(),
				NextStep: "",
			}
		}
		results = append(results, status)
	}

	return results, failed
}

func (app *application) writeIntegrationStatuses(results []install.IntegrationStatus) error {
	const (
		integrationStatusAgentWidth   = 12
		integrationStatusStateWidth   = 10
		integrationStatusMessageWidth = 60
		integrationStatusNextWidth    = 32
	)
	if app.outputJSON {
		return app.writeJSON(results)
	}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, []string{string(result.Harness), string(result.Status), result.Message, result.NextStep})
	}
	return app.writeWrappedHumanTable(
		[]humanColumn{{heading: "Agent", width: integrationStatusAgentWidth}, {heading: "Status", width: integrationStatusStateWidth}, {heading: "Message", width: integrationStatusMessageWidth}, {heading: "Next", width: integrationStatusNextWidth}},
		rows,
	)
}

func installIntegrations(ctx context.Context, args []string, opts integrationCommandOptions) ([]install.Result, error) {
	harnesses, err := selectedHarnesses(args, false)
	if err != nil {
		return nil, err
	}
	results := make([]install.Result, 0, len(harnesses))
	var failures []error
	for _, harnessID := range harnesses {
		result, installErr := install.RunContext(ctx, install.Options{Harness: harnessID, Binary: opts.binary, TargetBinary: opts.targetBinary, DryRun: opts.dryRun, Force: opts.force, UseShim: opts.shim})
		if installErr != nil {
			result = failedIntegrationResult(harnessID, "install failed", installErr)
			failures = append(failures, installErr)
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

func failedIntegrationResult(harnessID registry.Harness, message string, err error) install.Result {
	return install.Result{
		Harness:  string(harnessID),
		Path:     "",
		Changed:  false,
		Message:  message,
		NextStep: "",
		Snippet:  "",
		Error:    err.Error(),
	}
}

func (app *application) writeIntegrationResults(results []install.Result, showContent bool) error {
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		message := result.Message
		if result.Error != "" {
			message = result.Error
		}
		if result.NextStep != "" {
			message += "; next: " + result.NextStep
		}
		rows = append(rows, []string{result.Harness, strconv.FormatBool(result.Changed), result.Path, message})
	}
	columns := integrationResultTableColumns(rows, app.maxLineWidth())
	if err := app.writeWrappedHumanTable(columns, rows); err != nil {
		return err
	}
	if !showContent {
		return nil
	}
	for _, result := range results {
		if result.Snippet == "" {
			continue
		}
		if err := app.writef("\n%s generated content:\n", result.Harness); err != nil {
			return err
		}
		if err := app.writeln(result.Snippet); err != nil {
			return err
		}
	}
	return nil
}

func integrationResultTableColumns(rows [][]string, maxWidth int) []humanColumn {
	const (
		integrationResultColumns = 4
	)
	if maxWidth <= 0 {
		maxWidth = humanLineWidth
	}
	maxLen := []int{len("Agent"), len("Changed"), len("Path"), len("Result")}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(maxLen) {
				maxLen[i] = max(maxLen[i], text.StringWidth(cell))
			}
		}
	}

	agentWidth := maxLen[0]
	changedWidth := maxLen[1]
	gapsTotal := (integrationResultColumns - 1) * humanColumnGap
	fixedTotal := agentWidth + changedWidth + gapsTotal
	available := maxWidth - fixedTotal

	pathWidth, resultWidth := allocateIntegrationResultWidths(maxLen[2], maxLen[3], available)

	return []humanColumn{
		{heading: "Agent", width: agentWidth},
		{heading: "Changed", width: changedWidth},
		{heading: "Path", width: pathWidth, wrap: wrapHumanPath},
		{heading: "Result", width: resultWidth},
	}
}

func allocateIntegrationResultWidths(pathNeeded, resultNeeded, available int) (int, int) {
	const (
		minPathWidth   = 36
		minResultWidth = 30
	)
	pathMin := min(pathNeeded, minPathWidth)
	resultMin := min(resultNeeded, minResultWidth)

	switch {
	case available >= pathNeeded+resultNeeded:
		return pathNeeded, resultNeeded
	case available >= pathNeeded+resultMin:
		return pathNeeded, available - pathNeeded
	case available > pathMin+resultMin:
		extra := available - pathMin - resultMin
		pathUnmet := pathNeeded - pathMin
		resultUnmet := resultNeeded - resultMin
		totalUnmet := pathUnmet + resultUnmet
		if totalUnmet == 0 {
			return pathMin, resultMin
		}
		pathAdd := min(pathUnmet, extra*pathUnmet/totalUnmet)
		resultAdd := min(resultUnmet, extra-pathAdd)
		remaining := extra - pathAdd - resultAdd
		if remaining > 0 && pathMin+pathAdd < pathNeeded {
			canAdd := min(remaining, pathNeeded-(pathMin+pathAdd))
			pathAdd += canAdd
			remaining -= canAdd
		}
		if remaining > 0 && resultMin+resultAdd < resultNeeded {
			canAdd := min(remaining, resultNeeded-(resultMin+resultAdd))
			resultAdd += canAdd
		}
		return pathMin + pathAdd, resultMin + resultAdd
	default:
		return pathMin, resultMin
	}
}

func selectedHarnesses(args []string, emptyMeansAll bool) ([]registry.Harness, error) {
	if len(args) == 0 {
		if emptyMeansAll {
			return install.AllHarnesses(), nil
		}
		return nil, errAgentRequired
	}
	if len(args) == 1 && strings.EqualFold(args[0], "all") {
		return install.AllHarnesses(), nil
	}
	for _, arg := range args {
		if strings.EqualFold(arg, "all") {
			return nil, errAllWithAgents
		}
	}
	seen := make(map[registry.Harness]bool)
	result := make([]registry.Harness, 0, len(args))
	for _, arg := range args {
		harnessID, err := harnesspkg.Normalize(arg)
		if err != nil {
			return nil, fmt.Errorf("normalize agent: %w", err)
		}
		if seen[harnessID] {
			continue
		}
		seen[harnessID] = true
		result = append(result, harnessID)
	}
	return result, nil
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
	command.AddCommand(run, app.newTrackerEnableCommand(), app.newTrackerDisableCommand(), app.newTrackerStatusCommand())
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
	flags.DurationVar(&options.interval, "interval", options.interval, "reconciliation `<duration>`")
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
			options, err := app.parseServiceOptions(serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval, dryRun: dryRun})
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
	flags.DurationVar(&serviceConfig.interval, "interval", serviceConfig.interval, "expected reconciliation `<duration>`")
	flags.DurationVar(&serviceConfig.grace, "grace-period", serviceConfig.grace, "expected absence grace `<duration>`")
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
	command.AddCommand(app.newStatePathCommand(), app.newStateResetCommand(), app.newStateCleanCommand())
	return command
}

//nolint:gocognit,cyclop // clean selection validation, age calculations, confirmation, and garbage collection
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
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			options.ageSet = cmd.Flags().Changed("older-than")
			if options.all && options.ageSet {
				return exitCode(errCleanSelection, exitCodeUsage)
			}
			if !options.all && !options.ageSet && cfg.Retention.MaxGoneAge != "" {
				d, err := config.ParseDuration(cfg.Retention.MaxGoneAge)
				if err != nil {
					return fmt.Errorf("parsing max gone age: %w", err)
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
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.all && !yes {
				stdin := app.stdin
				if stdin == nil {
					stdin = os.Stdin
				}
				confirmed, err := app.confirmAction(cmd.Context(), stdin, "Delete all gone session records? [y/N]: ", "--yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return exitCode(errCleanAllConfirmation, exitCodeGeneral)
				}
			}
			return app.runRegistryClean(cmd.Context(), options)
		},
	}
	command.Flags().BoolVarP(&options.all, "all", "a", false, "delete every gone session record")
	command.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deleting all gone records without prompting")
	command.Flags().DurationVar(&options.olderThan, "older-than", 0, "delete gone records older than this `<duration>`")
	return command
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
			var session registry.Session
			var err error
			if options.paneID != "" {
				session, err = app.resolvePaneSession(cmd.Context(), options.paneID, options.serverID, options.multiplexerKind)
			} else {
				session, err = app.resolveSession(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			return app.writeInfo(cmd.Context(), session, options)
		},
	}
	command.Flags().BoolVar(&options.explain, "explain", false, "explain how the activity state was selected")
	command.Flags().StringVar(&options.paneID, "pane", "", "multiplexer pane `<id>`")
	command.Flags().StringVar(&options.serverID, "server", "", "multiplexer server `<id>`")
	command.Flags().StringVar(&options.multiplexerKind, "multiplexer", "", "multiplexer `<kind>`: tmux, zellij, herdr")
	command.Flags().StringVar(&options.configDir, "config-dir", "", "detection manifest override `<dir>`")
	command.Flags().BoolVar(&screenInspection, "screen-inspection", true, "enable terminal multiplexer screen inspection")
	return command
}

func (app *application) writeInfo(ctx context.Context, session registry.Session, opts infoOptions) error {
	if !opts.explain {
		if app.outputJSON {
			return app.writeJSON(session)
		}
		return app.writeSessionDetails(session)
	}
	explanation, explanationErr := evaluateExplanation(ctx, session, opts)
	if app.outputJSON {
		if err := app.writeJSON(explainedInfoResult{Session: session, Explanation: explanation}); err != nil {
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

func (app *application) resolveSession(ctx context.Context, reference string) (registry.Session, error) {
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
	var noSnapshot bool
	var watchFormat string
	var prepared watchOptions
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
	command.Flags().BoolVarP(&yes, "yes", "y", false, "confirm stopping all sessions without prompting")
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
		app.newConfigInitCommand(),
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
	return &cobra.Command{
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
			cfg := app.cfg
			if app.outputJSON {
				return app.writeJSON(cfg)
			}
			data, err := toml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("encode config toml: %w", err)
			}
			return app.writef("%s", string(data))
		},
	}
}

//nolint:gocognit,cyclop // manage config init path, stdin template output, directory checks, and publication
func (app *application) newConfigInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Initialize default configuration file",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.noConfig {
				return exitCode(errNoConfigDisallowsInit, exitCodeUsage)
			}
			if app.configPath == "-" && app.outputJSON {
				return exitCode(errInitJSONTemplateConflict, exitCodeUsage)
			}
			if app.configPath == "-" {
				return app.writef("%s", config.DefaultConfigTemplate())
			}

			path := app.configPath
			if path == "" {
				path = config.DefaultPath()
			}

			info, err := os.Stat(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w %s: %w", config.ErrAccessConfig, path, err)
			}
			if err == nil && info.IsDir() {
				return fmt.Errorf("%w: %s", config.ErrConfigIsDirectory, path)
			}
			if err == nil && !force {
				if app.outputJSON {
					return app.writeJSON(map[string]any{
						"created": false,
						"path":    path,
						"message": "config file already exists (use --force to overwrite)",
					})
				}
				app.warnf("config file already exists at %s (use --force to overwrite)\n", path)
				return nil
			}

			if err := config.WriteConfigFile(path); err != nil {
				return fmt.Errorf("init config: %w", err)
			}
			if app.outputJSON {
				return app.writeJSON(map[string]any{
					"created": true,
					"path":    path,
				})
			}
			app.warnf("created %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite existing configuration file")
	return cmd
}
