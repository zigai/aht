package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/manage"
)

type upgradeResult struct {
	Integrations []manage.IntegrationResult `json:"integrations"`
	Tracker      manage.TrackerResult       `json:"tracker"`
	//exhaustruct:optional
	TrackerError string `json:"tracker_error,omitempty"`
}

func (app *application) newUpgradeCommand() *cobra.Command {
	var (
		binary string
		dryRun bool
	)

	command := &cobra.Command{
		Use:           "upgrade",
		Short:         "Refresh installed integrations and tracker, preserving settings and running state",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager := app.manager(binary)

			integrations, integrationErr := manager.UpgradeIntegrations(
				cmd.Context(),
				manage.IntegrationOptions{DryRun: dryRun},
			)
			tracker, trackerErr := manager.UpgradeTracker(
				cmd.Context(),
				manage.TrackerOptions{DryRun: dryRun},
			)

			result := upgradeResult{Integrations: integrations, Tracker: tracker}
			if trackerErr != nil {
				result.TrackerError = trackerErr.Error()
			}

			var writeErr error
			if app.outputJSON {
				writeErr = app.writeJSON(result)
			} else {
				if len(integrations) == 0 {
					writeErr = app.writeln("integrations: none installed; skipped")
				} else {
					writeErr = app.writeIntegrationResults(integrations, false)
				}

				message := tracker.Message
				if trackerErr != nil {
					message = trackerErr.Error()
				}

				writeErr = errors.Join(writeErr, app.writef("tracker: %s\n", message))
			}

			if integrationErr != nil || trackerErr != nil {
				return exitCode(errors.Join(integrationErr, trackerErr, writeErr), exitCodeGeneral)
			}

			return writeErr
		},
	}
	command.Flags().
		StringVar(&binary, "binary", "", "new AHT binary `<path>` used by integrations and tracker")
	command.Flags().
		BoolVarP(&dryRun, "dry-run", "n", false, "preview upgrades without writing or restarting")

	return command
}
