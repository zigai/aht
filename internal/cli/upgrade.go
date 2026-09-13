package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
)

type upgradeResult struct {
	Integrations []install.Result `json:"integrations"`
	Tracker      service.Result   `json:"tracker"`
	TrackerError string           `json:"tracker_error,omitempty"`
}

func (app *application) newUpgradeCommand() *cli.Command {
	binary := defaultInstallBinary()
	var dryRun bool
	return &cli.Command{
		Name:  "upgrade",
		Usage: "Refresh installed integrations and tracker, preserving settings and running state",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "binary",
				Value:       binary,
				Destination: &binary,
				Usage:       "new aht binary used by integrations and tracker",
			},
			&cli.BoolFlag{
				Name:        "dry-run",
				Aliases:     []string{"n"},
				Destination: &dryRun,
				Usage:       "preview upgrades without writing or restarting",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 0 {
				return unexpectedArgsError(cmd.Args().Slice())
			}
			integrations, integrationErr := install.Upgrade(ctx, binary, dryRun)
			tracker, trackerErr := service.Upgrade(ctx, binary, dryRun)
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
}
