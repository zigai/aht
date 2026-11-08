package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/zigai/aht/v2/internal/config"
)

var (
	errCleanSelection       = errors.New("choose exactly one of --all or --older-than")
	errNegativeCleanAge     = errors.New("older-than must be nonnegative")
	errCleanAllConfirmation = errors.New(
		"cleaning all gone sessions was not confirmed (pass -y to confirm)",
	)
)

//exhaustruct:optional
type cleanOptions struct {
	all       bool
	olderThan time.Duration
	ageSet    bool
}

type manageResetResult struct {
	registry.ResetResult

	Path string `json:"path"`
}

func (app *application) newStatePathCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "path",
		Short:         "Print the registry state file path",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if app.outputJSON {
				return app.writeJSON(map[string]string{"path": app.resolvedStorePath()})
			}

			return app.writeln(app.resolvedStorePath())
		},
	}
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
	command.AddCommand(
		app.newStatePathCommand(),
		app.newStateResetCommand(),
		app.newStateCleanCommand(),
	)

	return command
}

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
			return app.prepareCleanOptions(cmd, &options)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runStateClean(cmd.Context(), options, yes)
		},
	}
	command.Flags().BoolVarP(&options.all, "all", "a", false, "delete every gone session record")
	command.Flags().
		BoolVarP(&yes, "yes", "y", false, "confirm deleting all gone records without prompting")
	command.Flags().
		DurationVar(&options.olderThan, "older-than", 0, "delete gone records older than this `<duration>`")

	return command
}

func (app *application) prepareCleanOptions(cmd *cobra.Command, options *cleanOptions) error {
	cfg, err := app.loadConfig()
	if err != nil {
		return err
	}

	options.ageSet = cmd.Flags().Changed("older-than")
	if options.all && options.ageSet {
		return exitCode(errCleanSelection, exitCodeUsage)
	}

	if !options.all && !options.ageSet && cfg.Retention.TombstoneTTL != "" {
		d, err := config.TombstoneTTL(cfg)
		if err != nil {
			return fmt.Errorf("parsing tombstone TTL: %w", err)
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
}

func (app *application) runStateClean(ctx context.Context, options cleanOptions, yes bool) error {
	if options.all && !yes {
		stdin := app.stdin
		if stdin == nil {
			stdin = os.Stdin
		}

		confirmed, err := app.confirmAction(
			ctx,
			stdin,
			"Delete all gone session records? [y/N]: ",
			"--yes",
		)
		if err != nil {
			return err
		}

		if !confirmed {
			return exitCode(errCleanAllConfirmation, exitCodeGeneral)
		}
	}

	return app.runRegistryClean(ctx, options)
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

func (app *application) newStateResetCommand() *cobra.Command {
	var force bool

	command := &cobra.Command{
		Use:           "reset",
		Short:         "Reset stored session state",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return exitCode(errStateResetForce, exitCodeUsage)
			}

			s := app.registryStore()

			r, e := s.Reset(cmd.Context())
			if e != nil {
				return e //nolint:wrapcheck // Client.Reset supplies the operation context.
			}

			o := manageResetResult{ResetResult: r, Path: s.StorePath()}
			if app.outputJSON {
				return app.writeJSON(o)
			}

			return app.writeHumanDetails([]humanDetail{
				{label: "Cleared", value: strconv.Itoa(o.Cleared)},
				{label: "Remaining", value: strconv.Itoa(o.Remaining)},
				{label: "Path", value: o.Path},
			})
		},
	}
	command.Flags().BoolVarP(&force, "force", "f", false, "confirm destructive state reset")

	return command
}
