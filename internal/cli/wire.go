package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
)

const wireHelp = `Run an owned Kimi Code process using its native Wire protocol.

Stdin and stdout carry native JSONL messages byte-for-byte. Connect an external
Wire client; this command is not an interactive terminal UI. Diagnostics and
native Kimi stderr go to stderr. AHT selects --wire; pass native Kimi options
only after --. Other harnesses and native UI modes are not supported.

Current managed Kimi hooks must already be installed with:
  aht manage integrations install kimi-code
The hooks supply native session/process identity; Wire supplies approval waiting.
The selected AHT --store is inherited by those hooks. No hooks are installed or
replaced by this command. Missing native identity stops the owned process safely.

Examples:
  aht hook wire kimi-code --
  aht --store /tmp/aht.json hook wire kimi-code -- --session SESSION_ID

Protocol: https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html`

var (
	errWireUnsupportedHarness  = errors.New("wire supports only kimi-code; use aht hook wire kimi-code -- [native Kimi options]")
	errWireMissingDashBoundary = errors.New("native Kimi options require the -- boundary; use aht hook wire kimi-code -- [native Kimi options]")
	errWireRequiresOSStreams   = errors.New("wire requires OS-backed stdin, stdout and stderr; connect the aht executable to your Wire client")
)

func (app *application) newWireCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "wire kimi-code -- [native Kimi options]",
		Short:         "Connect a native Kimi Wire client with tracked approval waiting",
		Long:          wireHelp,
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          app.validateWireArgs,
		RunE:          app.runWire,
	}
}

func (app *application) validateWireArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return exitCode(errWireUnsupportedHarness, exitCodeUsage)
	}
	harnessID, err := harnesscatalog.Normalize(args[0])
	if err != nil {
		return exitCode(errWireUnsupportedHarness, exitCodeUsage)
	}
	if cmd.ArgsLenAtDash() != 1 {
		return exitCode(errWireMissingDashBoundary, exitCodeUsage)
	}
	runner, ok := harnesscatalog.WireRunnerFor(harnessID)
	if !ok {
		return exitCode(errWireUnsupportedHarness, exitCodeUsage)
	}
	if err := runner.ValidateWireArgs(args[1:]); err != nil {
		return exitCode(err, exitCodeUsage)
	}
	return nil
}

func (app *application) runWire(cmd *cobra.Command, args []string) error {
	stdin := app.stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	inputFile, inputOK := stdin.(*os.File)
	stdout, outputOK := app.stdout.(*os.File)
	stderr, errorOK := app.stderr.(*os.File)
	if !inputOK || !outputOK || !errorOK {
		return errWireRequiresOSStreams
	}
	harnessID, err := harnesscatalog.Normalize(args[0])
	if err != nil {
		return exitCode(errWireUnsupportedHarness, exitCodeUsage)
	}
	runner, ok := harnesscatalog.WireRunnerFor(harnessID)
	if !ok {
		return exitCode(errWireUnsupportedHarness, exitCodeUsage)
	}
	if err := runner.RunWire(cmd.Context(), harness.WireOptions{
		Sink:      app.registryStore(),
		Args:      args[1:],
		StorePath: app.resolvedStorePath(),
		Stdin:     inputFile,
		Stdout:    stdout,
		Stderr:    stderr,
	}); err != nil {
		return fmt.Errorf("run wire: %w", err)
	}
	return nil
}
