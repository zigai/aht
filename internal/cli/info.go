package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errInfoReference = errors.New("provide one session reference or --pane")
	errInfoConfig    = errors.New("--config-dir requires --explain")
)

//exhaustruct:optional
type infoOptions struct {
	explain                 bool
	paneID                  string
	serverID                string
	multiplexerKind         string
	configDir               string
	disableScreenInspection bool
}

type explainedInfoResult struct {
	Session     registry.Session   `json:"session"`
	Explanation manage.Explanation `json:"explanation"`
}

func (app *application) writeSessionDetails(session registry.Session) error {
	rows := []humanDetail{
		{label: "ID", value: session.ID},
		{label: "Agent", value: string(session.Harness)},
		{label: "Presence", value: string(session.Presence())},
		{label: "Activity", value: formatActivity(session.Activity())},
		{label: "Session ID", value: session.SessionID},
		{label: "Session path", value: session.SessionPath},
		{label: "CWD", value: session.CWD},
		{label: "Project root", value: session.ProjectRoot},
		{label: "Resume command", value: strings.Join(session.ResumeCommand, " ")},
		{label: "Location", value: watchMultiplexerLabel(session.Location)},
		{label: "Created", value: session.CreatedAt.Format(time.RFC3339)},
		{label: "Updated", value: session.UpdatedAt.Format(time.RFC3339)},
	}
	if session.Process != nil {
		rows = append(
			rows,
			humanDetail{
				label: "Process",
				value: fmt.Sprintf(
					"pid=%d executable=%s",
					session.Process.PID,
					session.Process.Executable,
				),
			},
		)
	}

	return app.writeHumanDetails(rows)
}

func (app *application) newInfoCommand() *cobra.Command {
	options := infoOptions{}
	screenInspection := true
	command := &cobra.Command{
		Use:           "info [session]",
		Short:         "Show session details and optionally explain activity",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 0) == (options.paneID == "") {
				return exitCode(errInfoReference, exitCodeUsage)
			}

			if cmd.Flags().Changed("config-dir") && !options.explain {
				return exitCode(errInfoConfig, exitCodeUsage)
			}

			cfg, err := app.loadConfig()
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("config-dir") && cfg.Detection.ManifestsDir != "" {
				options.configDir = cfg.Detection.ManifestsDir
			}

			options.disableScreenInspection = false
			if cfg.Detection.ScreenInspection != nil && !*cfg.Detection.ScreenInspection {
				options.disableScreenInspection = true
			}

			if cmd.Flags().Changed("screen-inspection") {
				options.disableScreenInspection = !screenInspection
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				session registry.Session
				err     error
			)
			if options.paneID != "" {
				session, err = app.resolvePaneSession(
					cmd.Context(),
					options.paneID,
					options.serverID,
					options.multiplexerKind,
				)
			} else {
				session, err = app.resolveSession(cmd.Context(), args[0])
			}

			if err != nil {
				return err
			}

			return app.writeInfo(cmd.Context(), session, options)
		},
	}
	command.Flags().
		BoolVar(&options.explain, "explain", false, "explain how the activity state was selected")
	command.Flags().StringVar(&options.paneID, "pane", "", "multiplexer pane `<id>`")
	command.Flags().StringVar(&options.serverID, "server", "", "multiplexer server `<id>`")
	command.Flags().
		StringVar(&options.multiplexerKind, "multiplexer", "", "multiplexer `<kind>`: tmux, zellij, herdr")
	command.Flags().
		StringVar(&options.configDir, "config-dir", "", "detection manifest override `<dir>`")
	command.Flags().
		BoolVar(&screenInspection, "screen-inspection", true, "enable terminal multiplexer screen inspection")

	return command
}

func (app *application) writeInfo(
	ctx context.Context,
	session registry.Session,
	opts infoOptions,
) error {
	if !opts.explain {
		if app.outputJSON {
			return app.writeJSON(session)
		}

		return app.writeSessionDetails(session)
	}

	explanation, explanationErr := evaluateExplanation(ctx, session, opts)
	if app.outputJSON {
		if err := app.writeJSON(
			explainedInfoResult{Session: session, Explanation: explanation},
		); err != nil {
			return err
		}

		return explanationErr
	}

	if err := app.writeSessionDetails(session); err != nil {
		return err
	}

	if err := app.writef("\nActivity diagnosis:\n"); err != nil {
		return err
	}

	if err := app.writeExplanationDetails(explanation); err != nil {
		return err
	}

	return explanationErr
}

func (app *application) resolveSession(
	ctx context.Context,
	reference string,
) (registry.Session, error) {
	session, err := app.registryStore().Resolve(ctx, client.Selector{
		Reference: reference,
	})
	if err != nil {
		return registry.Session{}, fmt.Errorf("resolve session: %w", err)
	}

	return session, nil
}
