package cli

import (
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/config"
	"github.com/zigai/aht/v2/pkg/manage"
)

var (
	errInvalidObserveInterval    = errors.New("interval must be positive")
	errInvalidObserveGracePeriod = errors.New("grace period must be nonnegative")
)

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
	options := trackerFlags{interval: config.DefaultTrackerInterval}
	dryRun := false
	command := &cobra.Command{
		Use:           "enable",
		Short:         "Install, update, and start background tracking",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := app.trackerManager(cmd, options)
			if err != nil {
				return err
			}

			result, err := manager.EnableTracker(
				cmd.Context(),
				manage.TrackerOptions{DryRun: dryRun},
			)
			if err != nil {
				return managerError(err)
			}

			return app.writeTrackerResult(result)
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
	flags.BoolVarP(&dryRun, "dry-run", "n", false, "show changes without writing")

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
			result, err := app.manager("").DisableTracker(
				cmd.Context(),
				manage.TrackerOptions{DryRun: dryRun},
			)
			if err != nil {
				return managerError(err)
			}

			return app.writeTrackerResult(result)
		},
	}
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show changes without writing")

	return command
}

func (app *application) newTrackerStatusCommand() *cobra.Command {
	options := trackerFlags{interval: config.DefaultTrackerInterval}
	command := &cobra.Command{
		Use:           statusCommandName,
		Short:         "Show background tracking state",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := app.trackerManager(cmd, options)
			if err != nil {
				return err
			}

			result, err := manager.TrackerStatus(cmd.Context())
			if err != nil {
				return managerError(err)
			}

			return app.writeTrackerResult(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.binary, "binary", "", "expected AHT binary `<path>`")
	flags.DurationVar(
		&options.interval,
		"interval",
		options.interval,
		"expected reconciliation `<duration>`",
	)
	flags.DurationVar(
		&options.grace,
		"grace-period",
		options.grace,
		"expected absence grace `<duration>`",
	)

	return command
}

func (app *application) writeTrackerResult(result manage.TrackerResult) error {
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
