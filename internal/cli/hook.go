package cli

// Managed hook commands are integration entrypoints for harness-native
// request/response hooks. These hooks must write protocol JSON such as
// {"decision":"allow"} while recording session state, so they cannot use the
// one-way `aht report` command directly. Keep this file as CLI
// transport glue; harness protocol rules belong in internal/harness packages.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/internal/harness"
	harnesspkg "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

var (
	errUnsupportedManagedHook = errors.New("harness does not support managed hooks")
	errHookHarnessRequired    = errors.New("hook requires exactly one harness argument")
)

type managedHookOptions struct {
	event string
}

type observationSink interface {
	Observe(ctx context.Context, observation registry.Observation) (registry.Session, error)
}

func (app *application) newHookCommand() *cobra.Command {
	options := managedHookOptions{}

	cmd := &cobra.Command{
		Use:           hookCommandName + " <harness>",
		Short:         "Integration protocol endpoint; not intended for manual use",
		Long:          "Integration protocol endpoint; not intended for manual use. Hook stdout is a JSON protocol response, so --json is required.",
		Hidden:        true,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitCode(errHookHarnessRequired, exitCodeUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin := app.stdin
			if stdin == nil {
				stdin = os.Stdin
			}
			return app.runManagedHook(cmd.Context(), stdin, args[0], options)
		},
	}
	cmd.AddCommand(app.newWireCommand())
	cmd.Flags().StringVar(&options.event, "event", "", "native hook event `<name>`")
	return cmd
}

func (app *application) runManagedHook(
	ctx context.Context,
	stdin io.Reader,
	harnessName string,
	options managedHookOptions,
) error {
	if !app.outputJSON {
		return exitCode(errManagedHookJSONRequired, exitCodeUsage)
	}
	harness, err := harnesspkg.Normalize(harnessName)
	if err != nil {
		return fmt.Errorf("normalizing hook harness: %w", err)
	}

	data, err := readPayloadInput(stdin)
	if err != nil {
		return fmt.Errorf("reading hook payload: %w", err)
	}
	rawPayload := rawPayloadFromHookBytes(data)
	payload := hookPayloadObject(rawPayload)
	parentArgs := parentProcessArgs(ctx)
	result, ok := harnesspkg.HandleHook(harness, options.event, rawPayload, payload, parentArgs)
	if !ok {
		return fmt.Errorf("%w: %s", errUnsupportedManagedHook, harness)
	}
	if result.ReportOK {
		result.Report.SetProcess(reportProcessIdentity(harness, reportProcessAncestors(ctx, 0)))
	}

	if err := reportManagedHook(ctx, app.registryStore(), result); err != nil {
		app.warnf("warning: %v\n", err)
	}

	return app.writeJSON(result.Response)
}

func reportManagedHook(ctx context.Context, store observationSink, result harness.HookResult) error {
	if !result.ReportOK {
		return nil
	}
	observation := harnesspkg.PrepareObservation(result.Report)
	if collected, err := tmux.Current(ctx); err == nil {
		observation.SetLocation(&collected)
	}
	if collected := reportMultiplexerContext(); !collected.Empty() {
		observation.SetLocation(&collected)
	}
	if _, err := store.Observe(ctx, observation); err != nil {
		return fmt.Errorf("recording managed hook observation: %w", err)
	}
	return nil
}

func rawPayloadFromHookBytes(data []byte) json.RawMessage {
	payload, err := normalizeRawPayloadBytes(data)
	if err != nil {
		return nil
	}
	return payload
}

func hookPayloadObject(rawPayload json.RawMessage) map[string]any {
	if len(rawPayload) == 0 {
		return map[string]any{}
	}

	var payload map[string]any
	if err := json.Unmarshal(rawPayload, &payload); err != nil || payload == nil {
		return map[string]any{}
	}

	return payload
}
