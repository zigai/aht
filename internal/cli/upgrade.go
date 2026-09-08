package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
)

type upgradeResult struct {
	Integrations []install.Result `json:"integrations"`
	Tracker      service.Result   `json:"tracker"`
	TrackerError string           `json:"tracker_error,omitempty"`
}

func (app *application) newUpgradeCommand() *cobra.Command {
	binary := defaultInstallBinary()
	var dryRun bool
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Refresh installed integrations and tracker, preserving settings and running state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
					message = result.TrackerError
				}
				writeErr = errors.Join(writeErr, app.writef("tracker: %s\n", message))
			}
			if trackerErr != nil {
				trackerErr = fmt.Errorf("upgrade tracker: %w", trackerErr)
			}
			return errors.Join(integrationErr, trackerErr, writeErr)
		},
	}
	command.Flags().StringVar(&binary, "binary", binary, "new aht binary used by integrations and tracker")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview upgrades without writing or restarting")
	return command
}
