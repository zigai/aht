package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/internal/harness/kimi"
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
  aht wire kimi-code --
  aht --store /tmp/aht.json wire kimi-code -- --session SESSION_ID

Protocol: https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html`

var (
	errWireUnsupportedHarness  = errors.New("wire supports only kimi-code; use aht wire kimi-code -- [native Kimi options]")
	errWireMissingDashBoundary = errors.New("native Kimi options require the -- boundary; use aht wire kimi-code -- [native Kimi options]")
	errWireRequiresOSStreams   = errors.New("wire requires OS-backed stdin, stdout and stderr; connect the aht executable to your Wire client")
)

func (app *application) newWireCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "wire kimi-code -- [native Kimi options]",
		Short: "Connect a native Kimi Wire client with tracked approval waiting",
		Long:  wireHelp,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] != "kimi-code" {
				return errWireUnsupportedHarness
			}
			if cmd.ArgsLenAtDash() != 1 {
				return errWireMissingDashBoundary
			}
			return kimi.ValidateArgs(args[1:])
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, inputOK := cmd.InOrStdin().(*os.File)
			stdout, outputOK := app.stdout.(*os.File)
			stderr, errorOK := app.stderr.(*os.File)
			if !inputOK || !outputOK || !errorOK {
				return errWireRequiresOSStreams
			}
			return kimi.Run(cmd.Context(), kimi.Options{Args: args[1:], StorePath: app.resolvedStorePath(), Stdin: stdin, Stdout: stdout, Stderr: stderr})
		},
	}
}
