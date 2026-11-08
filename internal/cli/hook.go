package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/v2/internal/reporting"

	"github.com/spf13/cobra"
)

var errHookHarnessRequired = errors.New("hook requires exactly one harness argument")

var errManagedHookJSONRequired = errors.New(
	"hook commands require --json for their protocol response",
)

//exhaustruct:optional
type managedHookOptions struct {
	event string
}

func (app *application) newHookCommand() *cobra.Command {
	options := managedHookOptions{}

	cmd := &cobra.Command{
		Use:   hookCommandName + " <harness>",
		Short: "Integration protocol endpoint; not intended for manual use",
		Long: "Integration protocol endpoint; not intended for manual use. " +
			"Hook stdout is a JSON protocol response, so --json is " +
			"required.",
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitCode(errHookHarnessRequired, exitCodeUsage)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin := app.stdin
			if stdin == nil {
				stdin = os.Stdin
			}

			return app.runManagedHook(cmd.Context(), stdin, args[0], options)
		},
	}
	cmd.Flags().StringVar(&options.event, "event", "", "native hook event `<name>`")

	return cmd
}

func (app *application) runManagedHook(
	ctx context.Context,
	stdin io.Reader,
	harnessName string,
	opts managedHookOptions,
) error {
	if !app.outputJSON {
		return exitCode(errManagedHookJSONRequired, exitCodeUsage)
	}

	result, err := reporting.PrepareHook(ctx, stdin, harnessName, opts.event)
	if err != nil {
		return fmt.Errorf("preparing managed hook: %w", err)
	}

	if result.ReportOK {
		if _, err := app.registryStore().Observe(ctx, result.Report); err != nil {
			app.warnf("warning: recording managed hook observation: %v\n", err)
		}
	}

	return app.writeJSON(result.Response)
}
