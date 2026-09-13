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
	"github.com/urfave/cli/v3"

	"github.com/zigai/aht/internal/config"
	harnesspkg "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/registry"
)

var (
	errAllWithAgents            = errors.New("all cannot be combined with agent names")
	errAgentRequired            = errors.New("at least one agent or all is required")
	errCleanSelection           = errors.New("choose exactly one of --all or --older-than")
	errNegativeCleanAge         = errors.New("older-than must be nonnegative")
	errSessionReference         = errors.New("session reference is ambiguous")
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
	configDir               string
	disableScreenInspection bool
}

type explainedInfoResult struct {
	Session     registry.Session `json:"session"`
	Explanation explainResult    `json:"explanation"`
}

func (app *application) newSetupCommand() *cli.Command {
	options := integrationCommandOptions{binary: defaultInstallBinary()}
	serviceConfig := serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval}
	return &cli.Command{
		Name:      "setup",
		Usage:     "Set up harness integrations and start background tracking",
		ArgsUsage: "<agent... | all>",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "<agent... | all>", Desc: "One or more harness names (e.g. claude, codex, pi) or 'all'"},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       options.binary,
				Destination: &options.binary,
				Usage:       "AHT binary `path` used by integrations and tracker",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &options.dryRun,
				Usage:       "show changes without writing",
			},
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Destination: &options.force,
				Usage:       "replace foreign integration files",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return exitCode(errSetupHarnessRequired, exitCodeUsage)
			}
			args := cmd.Args().Slice()
			if _, err := selectedHarnesses(args, false); err != nil {
				return exitCode(err, exitCodeUsage)
			}
			serviceConfig.binary = options.binary
			serviceConfig.dryRun = options.dryRun
			serviceOptions, err := app.configuredServiceOptions(cmd, serviceConfig)
			if err != nil {
				return err
			}
			integrations, integrationErr := installIntegrations(ctx, args, options)
			tracker, trackerErr := runServiceOperation(ctx, "update", serviceOptions)
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
}

func (app *application) newIntegrationsCommand() *cli.Command {
	return &cli.Command{
		Name:  integrationsCommand,
		Usage: "Install, remove, and inspect agent integrations",
		Commands: []*cli.Command{
			app.newIntegrationsInstallCommand(),
			app.newIntegrationsRemoveCommand(),
			app.newIntegrationsStatusCommand(),
		},
	}
}

func (app *application) newIntegrationsInstallCommand() *cli.Command {
	options := integrationCommandOptions{binary: defaultInstallBinary()}
	return &cli.Command{
		Name:      installCommandName,
		Usage:     "Install or update agent integrations",
		ArgsUsage: "<agent... | all>",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "<agent... | all>", Desc: "One or more harness names or 'all'"},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       options.binary,
				Destination: &options.binary,
				Usage:       "AHT binary `path` used by installed integrations",
			},
			&cli.StringFlag{
				Name:        "target-binary",
				Destination: &options.targetBinary,
				Usage:       "Real agent binary `path` for shim installs",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &options.dryRun,
				Usage:       "show changes without writing",
			},
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Destination: &options.force,
				Usage:       "replace foreign integration files",
			},
			&cli.BoolFlag{
				Name:        "shim",
				Destination: &options.shim,
				Usage:       "install PATH shim instead of native hooks",
			},
			&cli.BoolFlag{
				Name:        "show-content",
				Destination: &options.showContent,
				Usage:       "show generated file content",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return exitCode(errInstallHarnessRequired, exitCodeUsage)
			}
			args := cmd.Args().Slice()
			if cmd.IsSet("target-binary") && !options.shim {
				return exitCode(errTargetBinaryNeedsShim, exitCodeUsage)
			}
			if cmd.IsSet("target-binary") && len(args) == 1 && strings.EqualFold(args[0], "all") {
				return exitCode(errTargetBinaryWithAll, exitCodeUsage)
			}
			results, err := installIntegrations(ctx, args, options)
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
}

func (app *application) newIntegrationsRemoveCommand() *cli.Command {
	options := integrationCommandOptions{}
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove aht-owned integrations",
		ArgsUsage: "<agent... | all>",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "<agent... | all>", Desc: "One or more harness names or 'all'"},
			},
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &options.dryRun,
				Usage:       "show changes without writing",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return exitCode(errRemoveHarnessRequired, exitCodeUsage)
			}
			harnesses, err := selectedHarnesses(cmd.Args().Slice(), false)
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}
			results := make([]install.Result, 0, len(harnesses))
			var failures []error
			for _, harnessID := range harnesses {
				result, removeErr := install.RemoveContext(ctx, install.Options{Harness: harnessID, Binary: options.binary, DryRun: options.dryRun})
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
}

func (app *application) newIntegrationsStatusCommand() *cli.Command {
	binary := defaultInstallBinary()
	return &cli.Command{
		Name:      statusCommandName,
		Usage:     "Show integration installation state",
		ArgsUsage: "[agent...]",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "[agent...]", Desc: "Optional harness names to inspect (inspects all if omitted)"},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       binary,
				Destination: &binary,
				Usage:       "Expected aht binary `path`",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return app.runIntegrationsStatus(ctx, cmd.Args().Slice(), binary)
		},
	}
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

func installIntegrations(ctx context.Context, args []string, options integrationCommandOptions) ([]install.Result, error) {
	harnesses, err := selectedHarnesses(args, false)
	if err != nil {
		return nil, err
	}
	results := make([]install.Result, 0, len(harnesses))
	var failures []error
	for _, harnessID := range harnesses {
		result, installErr := install.RunContext(ctx, install.Options{Harness: harnessID, Binary: options.binary, TargetBinary: options.targetBinary, DryRun: options.dryRun, Force: options.force, UseShim: options.shim})
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

func (app *application) newTrackerCommand() *cli.Command {
	run := app.newTrackerRunCommand()
	run.Usage = "Service entry point; not intended for manual use"
	return &cli.Command{
		Name:  trackerCommand,
		Usage: "Manage background session tracking",
		Commands: []*cli.Command{
			run,
			app.newTrackerEnableCommand(),
			app.newTrackerDisableCommand(),
			app.newTrackerStatusCommand(),
		},
	}
}

func (app *application) newTrackerEnableCommand() *cli.Command {
	options := serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval}
	return &cli.Command{
		Name:  "enable",
		Usage: "Install, update, and start background tracking",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       options.binary,
				Destination: &options.binary,
				Usage:       "AHT binary `path` run by the tracker",
			},
			&cli.DurationFlag{
				Name:        "interval",
				Value:       options.interval,
				Destination: &options.interval,
				Usage:       "Reconciliation `duration`",
			},
			&cli.DurationFlag{
				Name:        "grace-period",
				Value:       options.grace,
				Destination: &options.grace,
				Usage:       "Absence grace `duration`",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &options.dryRun,
				Usage:       "show changes without writing",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			parsed, err := app.configuredServiceOptions(cmd, options)
			if err != nil {
				return err
			}
			result, err := runServiceOperation(ctx, "update", parsed)
			if err != nil {
				return fmt.Errorf("enable tracker: %w", err)
			}
			return app.writeServiceResult(result)
		},
	}
}

func (app *application) newTrackerDisableCommand() *cli.Command {
	dryRun := false
	return &cli.Command{
		Name:  "disable",
		Usage: "Stop and remove background tracking",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &dryRun,
				Usage:       "show changes without writing",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			options, err := app.parseServiceOptions(serviceOptions{
				binary:   defaultInstallBinary(),
				interval: serviceDefaultInterval,
				dryRun:   dryRun,
			})
			if err != nil {
				return err
			}
			result, err := runServiceOperation(ctx, "uninstall", options)
			if err != nil {
				return fmt.Errorf("disable tracker: %w", err)
			}
			return app.writeServiceResult(result)
		},
	}
}

func (app *application) newTrackerStatusCommand() *cli.Command {
	serviceConfig := serviceOptions{binary: defaultInstallBinary(), interval: serviceDefaultInterval}
	return &cli.Command{
		Name:  statusCommandName,
		Usage: "Show background tracking state",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       serviceConfig.binary,
				Destination: &serviceConfig.binary,
				Usage:       "Expected aht binary `path`",
			},
			&cli.DurationFlag{
				Name:        "interval",
				Value:       serviceConfig.interval,
				Destination: &serviceConfig.interval,
				Usage:       "Expected reconciliation `duration`",
			},
			&cli.DurationFlag{
				Name:        "grace-period",
				Value:       serviceConfig.grace,
				Destination: &serviceConfig.grace,
				Usage:       "Expected absence grace `duration`",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			options, err := app.configuredServiceOptions(cmd, serviceConfig)
			if err != nil {
				return err
			}
			result, err := runServiceOperation(ctx, statusCommandName, options)
			if err != nil {
				return fmt.Errorf("tracker status: %w", err)
			}
			return app.writeServiceResult(result)
		},
	}
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

func (app *application) newStateCommand() *cli.Command {
	return &cli.Command{
		Name:  stateCommandName,
		Usage: "Inspect or clean stored session state",
		Commands: []*cli.Command{
			app.newRegistryPathCommand(),
			app.newRegistryResetCommand(),
			app.newRegistryCleanCommand(),
		},
	}
}

//nolint:gocognit,cyclop // clean selection validation, age calculations, confirmation, and garbage collection
func (app *application) newRegistryCleanCommand() *cli.Command {
	options := cleanOptions{}
	var yes bool
	return &cli.Command{
		Name:  "clean",
		Usage: "Delete gone session records",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "all",
				Aliases:     []string{"a"},
				Destination: &options.all,
				Usage:       "delete every gone session record",
			},
			&cli.BoolFlag{
				Name:        "yes",
				Aliases:     []string{"y"},
				Destination: &yes,
				Usage:       "confirm deleting all gone records without prompting",
			},
			&cli.DurationFlag{
				Name:        "older-than",
				Destination: &options.olderThan,
				Usage:       "Delete gone records older than this `duration`",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			options.ageSet = cmd.IsSet("older-than")
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
			if options.all && !yes {
				stdin := app.stdin
				if stdin == nil {
					stdin = os.Stdin
				}
				confirmed, err := app.confirmAction(ctx, stdin, "Delete all gone session records? [y/N]: ", "--yes")
				if err != nil {
					return err
				}
				if !confirmed {
					return exitCode(errCleanAllConfirmation, exitCodeGeneral)
				}
			}
			return app.runRegistryClean(ctx, options)
		},
	}
}

func (app *application) runRegistryClean(ctx context.Context, options cleanOptions) error {
	if options.all == options.ageSet {
		return exitCode(errCleanSelection, exitCodeUsage)
	}
	if options.olderThan < 0 {
		return exitCode(errNegativeCleanAge, exitCodeUsage)
	}
	age := options.olderThan
	if options.all {
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

//nolint:gocognit,cyclop // info session/pane resolution, config dir, screen inspection, and explanation
func (app *application) newInfoCommand() *cli.Command {
	options := infoOptions{}
	screenInspection := true
	return &cli.Command{
		Name:      "info",
		Usage:     "Show session details and optionally explain activity",
		ArgsUsage: "[session]",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "[session]", Desc: "Session ID, short ID prefix, name, or transcript path (optional if --pane is passed)"},
			},
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "explain",
				Destination: &options.explain,
				Usage:       "explain how the activity state was selected",
			},
			&cli.StringFlag{
				Name:        "pane",
				Destination: &options.paneID,
				Usage:       "Multiplexer pane `id`",
			},
			&cli.StringFlag{
				Name:        "config-dir",
				Destination: &options.configDir,
				Usage:       "Detection manifest override `dir`",
			},
			&cli.BoolFlag{
				Name:        "screen-inspection",
				Value:       true,
				Destination: &screenInspection,
				Usage:       "enable terminal multiplexer screen inspection",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) > 1 {
				return unexpectedArgsError(args[1:])
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if !cmd.IsSet("config-dir") && cfg.Detection.ManifestsDir != "" {
				options.configDir = cfg.Detection.ManifestsDir
			}
			options.disableScreenInspection = false
			if cfg.Detection.ScreenInspection != nil && !*cfg.Detection.ScreenInspection {
				options.disableScreenInspection = true
			}
			if cmd.IsSet("screen-inspection") {
				options.disableScreenInspection = !screenInspection
			}
			if (len(args) == 0) == (options.paneID == "") {
				return exitCode(errInfoReference, exitCodeUsage)
			}
			if cmd.IsSet("config-dir") && !options.explain {
				return exitCode(errInfoConfig, exitCodeUsage)
			}
			var session registry.Session
			if options.paneID != "" {
				session, err = app.resolvePaneSession(ctx, options.paneID)
			} else {
				session, err = app.resolveSession(ctx, args[0])
			}
			if err != nil {
				return err
			}
			return app.writeInfo(ctx, session, options)
		},
	}
}

func (app *application) writeInfo(ctx context.Context, session registry.Session, options infoOptions) error {
	if !options.explain {
		if app.outputJSON {
			return app.writeJSON(session)
		}
		return app.writeSessionDetails(session)
	}
	explanation, explanationErr := evaluateExplanation(ctx, session, options)
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
	sessions, err := app.registryStore().List(ctx, registry.Filter{})
	if err != nil {
		return registry.Session{}, fmt.Errorf("list sessions: %w", err)
	}
	matches := make([]registry.Session, 0, 1)
	for _, session := range sessions {
		if session.ID == reference {
			return session, nil
		}
		if strings.HasPrefix(session.ID, reference) || session.SessionID == reference || session.SessionPath == reference {
			matches = append(matches, session)
		}
	}
	if len(matches) == 0 {
		return registry.Session{}, registry.ErrSessionNotFound
	}
	if len(matches) > 1 {
		return registry.Session{}, fmt.Errorf("%w: %q matches %d sessions", errSessionReference, reference, len(matches))
	}
	return matches[0], nil
}

func (app *application) newWatchCommand() *cli.Command {
	options := listOptions{}
	var noSnapshot bool
	var watchFormat string
	return &cli.Command{
		Name:  "watch",
		Usage: "Stream session changes",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "agent",
				Destination: &options.harness,
				Usage:       "Filter by agent `name`",
			},
			&cli.StringFlag{
				Name:        "presence",
				Destination: &options.presence,
				Usage:       "Filter by presence (live, gone, unknown, all)",
			},
			&cli.StringFlag{
				Name:        "activity",
				Destination: &options.activity,
				Usage:       "Filter by reported activity (running, waiting, idle, unknown)",
			},
			&cli.StringFlag{
				Name:        "tmux-session",
				Destination: &options.tmuxSession,
				Usage:       "Filter by tmux session `name`",
			},
			&cli.StringFlag{
				Name:        "multiplexer-session",
				Destination: &options.multiplexerSession,
				Usage:       "Filter by multiplexer session `name`",
			},
			&cli.BoolFlag{
				Name:        "no-snapshot",
				Destination: &noSnapshot,
				Usage:       "start with future changes only",
			},
			&cli.StringFlag{
				Name:        "format",
				Destination: &watchFormat,
				Usage:       "Output format: `table|plain`",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
			if !cmd.IsSet("presence") && cfg.UI.DefaultPresence != "" {
				options.presence = cfg.UI.DefaultPresence
			}
			filter, err := buildFilter(options)
			if err != nil {
				return err
			}
			return app.runWatch(ctx, watchOptions{
				filter:     filter,
				agent:      options.harness,
				noSnapshot: noSnapshot,
				format:     watchFormat,
				formatSet:  cmd.IsSet("format"),
			})
		},
	}
}

func (app *application) newStopCommand() *cli.Command {
	all := false
	dryRun := false
	yes := false
	return &cli.Command{
		Name:      "stop",
		Usage:     "Gracefully stop sessions",
		ArgsUsage: "[session...]",
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "[session...]", Desc: "One or more session IDs, prefixes, or names to stop (or omit if using --all)"},
			},
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "all",
				Aliases:     []string{"a"},
				Destination: &all,
				Usage:       "stop every live session",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &dryRun,
				Usage:       "show targets without sending signals",
			},
			&cli.BoolFlag{
				Name:        "yes",
				Aliases:     []string{"y"},
				Destination: &yes,
				Usage:       "confirm stopping all sessions without prompting",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
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
				confirmed, err := app.confirmStopAll(ctx, stdin)
				if err != nil {
					return err
				}
				if !confirmed {
					return exitCode(errStopAllConfirmation, exitCodeGeneral)
				}
			}
			return app.runStop(ctx, args, all, dryRun)
		},
	}
}

func (app *application) newManageCommand() *cli.Command {
	return &cli.Command{
		Name:  "manage",
		Usage: "Manage setup, integrations, tracking, and state",
		Commands: []*cli.Command{
			app.newSetupCommand(),
			app.newUpgradeCommand(),
			app.newIntegrationsCommand(),
			app.newTrackerCommand(),
			app.newStateCommand(),
			app.newDoctorCommand(),
			app.newManageConfigCommand(),
		},
	}
}

func (app *application) newManageConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Inspect configuration path and effective settings",
		Commands: []*cli.Command{
			app.newManageConfigPathCommand(),
			app.newManageConfigShowCommand(),
			app.newManageConfigInitCommand(),
		},
	}
}

func (app *application) newManageConfigPathCommand() *cli.Command {
	return &cli.Command{
		Name:  "path",
		Usage: "Print resolved configuration file path",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			_, err := app.loadConfig()
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
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

func (app *application) newManageConfigShowCommand() *cli.Command {
	return &cli.Command{
		Name:  "show",
		Usage: "Print effective configuration",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}
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
func (app *application) newManageConfigInitCommand() *cli.Command {
	var force bool
	return &cli.Command{
		Name:  "init",
		Usage: "Initialize default configuration file",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "force",
				Aliases:     []string{"f"},
				Destination: &force,
				Usage:       "overwrite existing configuration file",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			if app.noConfig {
				return exitCode(errNoConfigDisallowsInit, exitCodeUsage)
			}
			path := app.configPath
			if path == "-" {
				if app.outputJSON {
					return exitCode(errInitJSONTemplateConflict, exitCodeUsage)
				}
				return app.writef("%s", config.DefaultConfigTemplate())
			}
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
				// F10: Notice goes to stderr
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
			// F10: Notice goes to stderr
			app.warnf("created %s\n", path)
			return nil
		},
	}
}
