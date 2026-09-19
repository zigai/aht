package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

type waitFlags struct {
	activity    string
	presence    string
	timeout     time.Duration
	stableFor   time.Duration
	hasActivity bool
	hasPresence bool
}

func (app *application) newWaitCommand() *cobra.Command {
	var flags waitFlags

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
	cmdFlags.StringVar(&flags.activity, "activity", "", "wait for activity `<val>`: running, waiting, idle, failed, interrupted, unknown")
	cmdFlags.StringVar(&flags.presence, "presence", "", "wait for presence `<val>`: live, gone, unknown")
	cmdFlags.DurationVar(&flags.timeout, "timeout", 0, "maximum time to wait `<duration>`")
	cmdFlags.DurationVar(&flags.stableFor, "stable-for", 0, "duration condition must hold continuously `<duration>`")

	return command
}

func (app *application) executeWait(ctx context.Context, sessionArg string, flags waitFlags) error {
	sessionRef := strings.TrimSpace(sessionArg)
	if sessionRef == "" {
		return exitCode(client.ErrSessionRequired, exitCodeUsage)
	}

	options, err := parseWaitConditions(flags)
	if err != nil {
		return err
	}

	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	session, err := app.resolveSession(ctx, sessionRef)
	if err != nil {
		return err
	}
	options.ID = session.ID

	ahtClient := app.registryStore()
	res, err := ahtClient.Wait(ctx, options)
	if err != nil {
		return fmt.Errorf("waiting for session: %w", err)
	}

	if app.outputJSON {
		return app.writeJSON(res.Session)
	}
	return app.writeSessionDetails(res.Session)
}

func parseWaitConditions(flags waitFlags) (client.WaitOptions, error) {
	if !flags.hasActivity && !flags.hasPresence {
		return client.WaitOptions{}, exitCode(client.ErrConditionRequired, exitCodeUsage)
	}
	if flags.timeout < 0 {
		return client.WaitOptions{}, exitCode(fmt.Errorf("%w: timeout cannot be negative", client.ErrInvalidDuration), exitCodeUsage)
	}
	if flags.stableFor < 0 {
		return client.WaitOptions{}, exitCode(fmt.Errorf("%w: stable-for cannot be negative", client.ErrInvalidDuration), exitCodeUsage)
	}
	if flags.timeout > 0 && flags.stableFor > flags.timeout {
		return client.WaitOptions{}, exitCode(fmt.Errorf("%w: stable-for duration cannot exceed timeout", client.ErrInvalidDuration), exitCodeUsage)
	}

	presence, err := parseTargetPresence(flags.presence, flags.hasPresence)
	if err != nil {
		return client.WaitOptions{}, err
	}

	activity, err := parseTargetActivity(flags.activity, flags.hasActivity)
	if err != nil {
		return client.WaitOptions{}, err
	}

	if err := validateConditionCombination(presence, activity); err != nil {
		return client.WaitOptions{}, err
	}

	return client.WaitOptions{
		ID:        "",
		Activity:  activity,
		Presence:  presence,
		Timeout:   flags.timeout,
		StableFor: flags.stableFor,
	}, nil
}

func parseTargetPresence(presence string, changed bool) (registry.Presence, error) {
	if !changed {
		return "", nil
	}
	p, err := registry.NormalizePresence(presence)
	if err != nil {
		return "", exitCode(err, exitCodeUsage)
	}
	return p, nil
}

func parseTargetActivity(activity string, changed bool) (registry.Activity, error) {
	if !changed {
		return "", nil
	}
	a, err := registry.NormalizeActivity(activity)
	if err != nil {
		return "", exitCode(err, exitCodeUsage)
	}
	return a, nil
}

func validateConditionCombination(presence registry.Presence, activity registry.Activity) error {
	if (presence == registry.PresenceGone || presence == registry.PresenceUnknown) && activity != "" {
		return exitCode(fmt.Errorf("%w: presence %q cannot be combined with activity %q", client.ErrContradictoryCondition, presence, activity), exitCodeUsage)
	}
	return nil
}
