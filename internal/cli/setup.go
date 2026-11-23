package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/manage"
)

var errSetupHarnessRequired = errors.New("setup requires at least one harness or 'all'")

type setupResult struct {
	Integrations []manage.IntegrationResult `json:"integrations"`
	Tracker      manage.TrackerResult       `json:"tracker"`
	//exhaustruct:optional
	TrackerError string `json:"tracker_error,omitempty"`
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
			return app.runSetup(cmd.Context(), args, options)
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

func (app *application) runSetup(
	ctx context.Context,
	args []string,
	opts integrationCommandOptions,
) error {
	harnesses, err := selectedHarnesses(args, false)
	if err != nil {
		return exitCode(err, exitCodeUsage)
	}

	manager := app.manager(opts.binary)
	if err := manager.ValidateConfig(); err != nil {
		return managerError(err)
	}

	integrations, integrationErr := installIntegrations(ctx, manager, harnesses, opts)

	tracker, trackerErr := manager.EnableTracker(
		ctx,
		manage.TrackerOptions{DryRun: opts.dryRun},
	)
	trackerErr = managerError(trackerErr)

	if err := app.writeSetupOutcome(integrations, tracker, integrationErr, trackerErr); err != nil {
		return err
	}

	return errors.Join(integrationErr, trackerErr)
}

func (app *application) writeSetupOutcome(
	integrations []manage.IntegrationResult,
	tracker manage.TrackerResult,
	integrationErr error,
	trackerErr error,
) error {
	result := setupResult{Integrations: integrations, Tracker: tracker}
	if trackerErr != nil {
		result.TrackerError = trackerErr.Error()
	}

	if app.outputJSON {
		return app.writeJSON(result)
	}

	if err := app.writeIntegrationResults(integrations, false); err != nil {
		return err
	}

	trackerMessage := tracker.Message
	if trackerErr != nil {
		trackerMessage = trackerErr.Error()
	}

	if err := app.writef("tracker: %s\n", trackerMessage); err != nil {
		return err
	}

	if integrationErr == nil && trackerErr == nil {
		return app.writeln("next: aht list")
	}

	return nil
}
