package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/v2/internal/reporting"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/harness/catalog"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (app *application) newReportCommand() *cobra.Command {
	options := reporting.Options{CWDAuto: true}

	var quiet bool

	cmd := &cobra.Command{
		Use:           "report [harness]",
		Short:         "Record a harness observation",
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				options.Harness = args[0]
			}

			if cmd.Flags().Changed("cwd") {
				options.CWDAuto = false
			}

			reporting.ApplyHarnessEnv(&options)

			stdin := app.stdin
			if stdin == nil {
				stdin = os.Stdin
			}

			return app.runReport(cmd.Context(), stdin, options, quiet)
		},
	}
	f := cmd.Flags()
	f.StringVar(
		&options.DetailObservedAt,
		"detail-observed-at",
		"",
		"original detail evidence RFC3339 timestamp",
	)
	f.StringVar(
		&options.Detail,
		"detail",
		"",
		"activity detail: permission, question, general, usage_limit, or clear",
	)
	f.StringVar(&options.Reporter, "reporter", options.Reporter, "reporting integration identity")
	f.IntVar(
		&options.ReporterVersion,
		"reporter-version",
		options.ReporterVersion,
		"reporting integration version",
	)
	f.BoolVar(
		&options.MultiSession,
		"multi-session",
		options.MultiSession,
		"reporter supports multiple sessions per process",
	)
	f.StringVar(
		&options.Presence,
		"presence",
		options.Presence,
		"presence: `<val>` (live, gone, unknown)",
	)
	f.StringVar(
		&options.Activity,
		"activity",
		options.Activity,
		"reported activity hint: `<val>` (running, waiting, idle, unknown)",
	)
	f.StringVar(
		&options.Lifecycle,
		"lifecycle",
		options.Lifecycle,
		"native lifecycle: `<val>` (start, resume, end)",
	)
	_ = f.MarkHidden("lifecycle")
	f.StringVar(&options.SessionID, "session-id", options.SessionID, "harness session `<id>`")
	f.StringVar(
		&options.SessionPath,
		"session-path",
		options.SessionPath,
		"harness session file `<path>`",
	)
	f.StringVar(&options.CWD, "cwd", options.CWD, "agent current working `<dir>`")
	f.StringVar(&options.ProjectRoot, "project-root", options.ProjectRoot, "project `<root>`")
	f.IntVar(&options.PID, "pid", options.PID, "agent process `<id>`")
	f.IntVar(&options.PPID, "ppid", options.PPID, "agent parent process `<id>`")
	f.IntVar(
		&options.ProcessGroupID,
		"process-group-id",
		options.ProcessGroupID,
		"agent process group `<id>`",
	)
	f.StringVar(
		&options.StartIdentity,
		"start-identity",
		options.StartIdentity,
		"process start `<identity>`",
	)
	f.StringVar(
		&options.Executable,
		"executable",
		options.Executable,
		"resolved executable `<path>`",
	)
	f.StringVar(&options.TTY, "tty", options.TTY, "agent `<tty>`")
	f.StringVar(&options.Event, "event", options.Event, "native harness event `<name>`")
	f.StringVar(&options.ObservedAt, "observed-at", options.ObservedAt, "RFC3339 `<timestamp>`")
	f.StringVar(
		&options.Sequence,
		"sequence",
		options.Sequence,
		"strictly increasing integration report `<seq>`",
	)
	f.StringArrayVar(&options.Attributes, "attribute", nil, "extra `<key=value>` attribute")
	f.StringArrayVar(
		&options.ResumeCommand,
		"resume-command",
		nil,
		"resume command argv `<item>`, repeatable",
	)
	f.StringVar(
		&options.Evidence,
		"evidence",
		options.Evidence,
		"evidence `<kind>` (managed shims)",
	)
	f.BoolVar(
		&options.RawStdin,
		"raw-stdin",
		false,
		"store stdin as the hook payload, without tool input and output",
	)
	f.BoolVar(
		&options.RawDefaultsOnly,
		"raw-stdin-defaults-only",
		false,
		"read stdin for defaults without storing raw payload",
	)
	f.BoolVar(&options.NoTmux, "no-tmux", false, "do not collect tmux context")
	_ = f.MarkHidden("evidence")
	f.BoolVarP(&quiet, "quiet", "q", false, "suppress human-readable output")

	return cmd
}

func (app *application) runReport(
	ctx context.Context,
	stdin io.Reader,
	opts reporting.Options,
	quiet bool,
) error {
	prepared, err := reporting.Prepare(
		stdin,
		opts,
		reporting.CollectRuntime(ctx, opts.PID, opts.NoTmux),
	)
	if err != nil {
		if _, ok := errors.AsType[*reporting.InputError](err); ok {
			return exitCode(err, exitCodeUsage)
		}

		return fmt.Errorf("preparing report: %w", err)
	}

	if prepared.Ignored {
		return app.writeReportIgnored(
			prepared.Harness,
			"payload_mismatch",
			"hook payload does not match harness",
			quiet,
		)
	}

	session, err := app.registryStore().Observe(ctx, prepared.Observation)
	if errors.Is(err, registry.ErrProcessEnded) {
		// A late hook from an exited process is expected, not a hook failure.
		return app.writeReportIgnored(
			prepared.Harness,
			"process_ended",
			"reporting process already ended",
			quiet,
		)
	}

	if err != nil {
		return err //nolint:wrapcheck // Client.Observe supplies the operation context.
	}

	return app.writeReportResult(session, quiet)
}

func (app *application) writeReportIgnored(
	harnessID registry.Harness,
	reason string, detail string,
	quiet bool,
) error {
	if app.outputJSON {
		return app.writeJSON(
			map[string]string{
				statusCommandName: "ignored",
				"harness":         string(harnessID),
				"reason":          reason,
			},
		)
	}

	if quiet {
		return nil
	}

	return app.writef("ignored %s report: %s\n", harnessID, detail)
}

func (app *application) writeReportResult(session registry.Session, quiet bool) error {
	const (
		reportIDWidth            = 30
		reportAgentWidth         = 12
		reportPresenceWidth      = 10
		reportActivityWidth      = 12
		reportAuthoritativeWidth = 13
	)

	if app.outputJSON {
		return app.writeJSON(session)
	}

	if quiet {
		return nil
	}

	reportedActivity := "-"
	authoritative := "-"

	if native := session.Observations.Native; native != nil && native.Activity != nil {
		reportedActivity = string(*native.Activity)

		authoritative = "no"

		policy := (catalog.Rules{}).Policy(session.Harness)
		if policy.Authority == registry.AuthorityHook && !terminalDecides(session) {
			authoritative = "yes"
		}
	}

	return app.writeHumanTable(
		[]humanColumn{
			{heading: "ID", width: reportIDWidth},
			{heading: "Agent", width: reportAgentWidth},
			{heading: "Presence", width: reportPresenceWidth},
			{heading: "Reported", width: reportActivityWidth},
			{heading: "Effective", width: reportActivityWidth},
			{heading: "Authoritative", width: reportAuthoritativeWidth},
		},
		[][]string{
			{
				session.ID,
				string(session.Harness),
				string(session.Presence()),
				reportedActivity,
				formatActivity(session.Activity()),
				authoritative,
			},
		},
	)
}

func terminalDecides(session registry.Session) bool {
	decision := session.Decision()

	return decision != nil && decision.Authority == registry.AuthorityTerminal
}
