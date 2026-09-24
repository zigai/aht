package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/install"
)

func (app *application) newIntegrationsUpgradeCommand() *cobra.Command {
	var binary string
	var dryRun bool
	command := &cobra.Command{Use: "upgrade", Short: "Refresh installed integrations while preserving native settings", Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
		if binary == "" {
			binary = defaultInstallBinary()
		}
		results, err := install.Upgrade(cmd.Context(), binary, dryRun)
		var writeErr error
		if app.outputJSON {
			writeErr = app.writeJSON(results)
		} else {
			writeErr = app.writeIntegrationResults(results, false)
		}
		if err != nil {
			return exitCode(errors.Join(err, writeErr), exitCodeGeneral)
		}
		return writeErr
	}}
	command.Flags().StringVar(&binary, "binary", "", "AHT binary `<path>` used by installed integrations")
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "preview integration upgrades without writing")
	return command
}
