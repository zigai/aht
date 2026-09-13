package cli

import (
	"context"
	"errors"
	"os"

	"github.com/urfave/cli/v3"

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
  aht hook wire kimi-code --
  aht --store /tmp/aht.json hook wire kimi-code -- --session SESSION_ID

Protocol: https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html`

var (
	errWireUnsupportedHarness  = errors.New("wire supports only kimi-code; use aht hook wire kimi-code -- [native Kimi options]")
	errWireMissingDashBoundary = errors.New("native Kimi options require the -- boundary; use aht hook wire kimi-code -- [native Kimi options]")
	errWireRequiresOSStreams   = errors.New("wire requires OS-backed stdin, stdout and stderr; connect the aht executable to your Wire client")
)

//nolint:cyclop // raw argument parsing, OS stream verification, and process startup
func (app *application) newWireCommand() *cli.Command {
	return &cli.Command{
		Name:            "wire",
		Usage:           "Connect a native Kimi Wire client with tracked approval waiting",
		ArgsUsage:       "kimi-code -- [native Kimi options]",
		Description:     wireHelp,
		SkipFlagParsing: true,
		Metadata: map[string]any{
			helpArgumentsKey: []HelpArg{
				{Name: "kimi-code", Desc: "Target harness (kimi-code is currently the only supported Wire harness)"},
				{Name: "--", Desc: "Boundary delimiter separating AHT options from native Kimi options"},
				{Name: "[options]", Desc: "Native Kimi Code options forwarded to the owned process"},
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()
			if len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
				cli.HelpPrinter(cmd.Root().Writer, "", cmd)
				return nil
			}
			if len(args) == 0 || args[0] != "kimi-code" {
				return exitCode(errWireUnsupportedHarness, exitCodeUsage)
			}
			if len(args) < 2 || args[1] != "--" {
				return exitCode(errWireMissingDashBoundary, exitCodeUsage)
			}
			nativeArgs := args[2:]
			if err := kimi.ValidateArgs(nativeArgs); err != nil {
				return exitCode(err, exitCodeUsage)
			}
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
			return kimi.Run(ctx, kimi.Options{
				Args:      nativeArgs,
				StorePath: app.resolvedStorePath(),
				Stdin:     inputFile,
				Stdout:    stdout,
				Stderr:    stderr,
			})
		},
	}
}
