package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"

	harnesspkg "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/pkg/registry"
)

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
		app.newIntegrationsUpgradeCommand(),
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
