package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/manage"
)

func (app *application) newIntegrationsUpgradeCommand() *cobra.Command {
	var (
		binary string
		dryRun bool
	)

	command := &cobra.Command{
		Use:   "upgrade [agent]",
		Short: "Refresh installed integrations while preserving native settings",
		Args: func(cmd *cobra.Command, args []string) error {
			return exitCode(cobra.MaximumNArgs(1)(cmd, args), exitCodeUsage)
		},
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := app.upgradeIntegrations(cmd.Context(), args, binary, dryRun)

			if _, usage := errors.AsType[*exitCodeError](err); usage {
				return err
			}

			var writeErr error
			if app.outputJSON {
				writeErr = app.writeJSON(results)
			} else {
				writeErr = app.writeIntegrationResults(results, false)
			}

			if err != nil {
				return exitCode(errors.Join(err, writeErr), exitCodeGeneral)
			}

			return writeErr
		},
	}
	command.Flags().
		StringVar(&binary, "binary", "", "AHT binary `<path>` used by installed integrations")
	command.Flags().
		BoolVarP(&dryRun, "dry-run", "n", false, "preview integration upgrades without writing")

	return command
}

func (app *application) upgradeIntegrations(
	ctx context.Context,
	args []string,
	binary string,
	dryRun bool,
) ([]manage.IntegrationResult, error) {
	manager := app.manager(binary)
	opts := manage.UpgradeOptions{DryRun: dryRun}

	if len(args) == 0 || strings.EqualFold(args[0], "all") {
		return manager.UpgradeIntegrations(ctx, opts)
	}

	harnesses, err := selectedHarnesses(args, false)
	if err != nil {
		return nil, exitCode(err, exitCodeUsage)
	}

	return manager.UpgradeIntegration(ctx, harnesses[0], opts)
}
