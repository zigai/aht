package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/internal/service"
)

type upgradeResult struct {
	Integrations []install.Result `json:"integrations"`
	Tracker      service.Result   `json:"tracker"`
	TrackerError string           `json:"tracker_error,omitempty"`
}

func (app *application) newUpgradeCommand() *cobra.Command {
	var binary string
	var dryRun bool
	command := &cobra.Command{
		Use:           "upgrade",
		Short:         "Refresh installed integrations and tracker, preserving settings and running state",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if binary == "" {
				binary = defaultInstallBinary()
			}
			integrations, integrationErr := install.Upgrade(cmd.Context(), binary, dryRun)
			tracker, trackerErr := service.Upgrade(cmd.Context(), binary, dryRun)
			if errors.Is(trackerErr, service.ErrUnsupported) {
				tracker.Message = "unsupported platform; skipped"
				trackerErr = nil
			}
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
			return errors.Join(integrationErr, trackerErr, writeErr)
		},
	}
	command.Flags().StringVar(&binary, "binary", "", "new AHT binary `<path>` used by integrations and tracker")
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview upgrades without writing or restarting")
	return command
}
