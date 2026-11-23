package reporting

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
)

var errUnsupportedManagedHook = errors.New("harness does not support managed hooks")

func PrepareHook(
	ctx context.Context,
	stdin io.Reader,
	harnessName string, event string,
) (harness.HookResult, error) {
	id, err := adapters.Parse(harnessName)
	if err != nil {
		return harness.HookResult{}, fmt.Errorf("normalizing hook harness: %w", err)
	}

	data, err := readReportPayloadInput(stdin, id)
	if err != nil {
		return harness.HookResult{}, fmt.Errorf("reading hook payload: %w", err)
	}

	raw, err := normalizeRawPayloadBytes(data)
	if err != nil {
		return harness.HookResult{}, fmt.Errorf("encoding hook payload: %w", err)
	}

	payload, _ := harness.PayloadObject(raw)

	result, ok, err := adapters.HandleHook(ctx, id, event, raw, payload, parentProcessArgs(ctx))
	if !ok {
		return harness.HookResult{}, fmt.Errorf("%w: %s", errUnsupportedManagedHook, id)
	}

	if err != nil {
		return harness.HookResult{}, fmt.Errorf("handling managed hook: %w", err)
	}

	if result.ReportOK {
		runtime := CollectRuntime(ctx, 0, false)
		result.Report.SetProcess(processIdentity(id, runtime.Processes))

		result.Report = adapters.PrepareObservation(result.Report)
		if location := runtime.reportLocation(id); !location.Empty() {
			result.Report.SetLocation(&location)
		}
	}

	return result, nil
}
