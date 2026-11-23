package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

type waitOptions struct {
	activity    string
	presence    string
	timeout     time.Duration
	stableFor   time.Duration
	hasActivity bool
	hasPresence bool
}

func (app *application) newWaitCommand() *cobra.Command {
	var flags waitOptions

	command := &cobra.Command{
		Use:           "wait <session>",
		Short:         "Wait for a session condition",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.hasActivity = cmd.Flags().Changed("activity")
			flags.hasPresence = cmd.Flags().Changed("presence")

			return app.executeWait(cmd.Context(), args[0], flags)
		},
	}

	cmdFlags := command.Flags()
	cmdFlags.StringVar(
		&flags.activity,
		"activity",
		"",
		"wait for activity `<val>`: running, waiting, idle, failed, interrupted, unknown",
	)
	cmdFlags.StringVar(
		&flags.presence,
		"presence",
		"",
		"wait for presence `<val>`: live, gone (also met when the tracker removes the session), unknown",
	)
	cmdFlags.DurationVar(&flags.timeout, "timeout", 0, "maximum time to wait `<duration>`")
	cmdFlags.DurationVar(
		&flags.stableFor,
		"stable-for",
		0,
		"duration condition must hold continuously `<duration>`",
	)

	return command
}

func (app *application) executeWait(
	ctx context.Context,
	sessionArg string,
	opts waitOptions,
) error {
	options, err := parseWaitOptions(strings.TrimSpace(sessionArg), opts)
	if err != nil {
		return exitCode(err, exitCodeUsage)
	}

	res, err := app.registryStore().Wait(ctx, options)
	if err != nil {
		return fmt.Errorf("waiting for session: %w", err)
	}

	if app.outputJSON {
		return app.writeJSON(res.Session)
	}

	return app.writeSessionDetails(res.Session)
}

func parseWaitOptions(reference string, opts waitOptions) (client.WaitOptions, error) {
	options := client.WaitOptions{
		Reference: reference,
		Activity:  "",
		Presence:  "",
		Timeout:   opts.timeout,
		StableFor: opts.stableFor,
	}

	if opts.hasPresence {
		presence, err := registry.ParsePresence(opts.presence)
		if err != nil {
			return options, fmt.Errorf("normalize presence: %w", err)
		}

		options.Presence = presence
	}

	if opts.hasActivity {
		activity, err := registry.ParseActivity(opts.activity)
		if err != nil {
			return options, fmt.Errorf("normalize activity: %w", err)
		}

		options.Activity = activity
	}

	if err := options.Validate(); err != nil {
		return options, fmt.Errorf("invalid wait: %w", err)
	}

	return options, nil
}
