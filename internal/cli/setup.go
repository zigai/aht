package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/manage"
)

var errSetupHarnessRequired = errors.New("setup requires at least one harness or 'all'")

type setupResult struct {
	Integrations []manage.IntegrationResult `json:"integrations"`
	Tracker      manage.TrackerResult       `json:"tracker"`
}

func (app *application) newSetupCommand() *cobra.Command {
	options := integrationCommandOptions{}
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
		RunE: func(cmd *cobra.Command, args []string) error {
			harnesses, err := selectedHarnesses(args, false)
			if err != nil {
				return exitCode(err, exitCodeUsage)
			}

			manager := app.manager(options.binary)
			if err := manager.ValidateConfig(); err != nil {
				return managerError(err)
			}

			integrations, integrationErr := installIntegrations(
				cmd.Context(),
				manager,
				harnesses,
				options,
			)

			tracker, trackerErr := manager.EnableTracker(
				cmd.Context(),
				manage.TrackerOptions{DryRun: options.dryRun},
			)
			trackerErr = managerError(trackerErr)

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
