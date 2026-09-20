package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (app *application) newCurrentCommand() *cobra.Command {
	command := &cobra.Command{
		Use:           "current",
		Short:         "Show the agent session for the current calling context",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			_, err := app.loadConfig()
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, err := app.registryStore().Current(cmd.Context())
			if err != nil {
				return fmt.Errorf("current session: %w", err)
			}
			if app.outputJSON {
				return app.writeJSON(session)
			}
			return app.writeSessionDetails(session)
		},
	}
	return command
}
