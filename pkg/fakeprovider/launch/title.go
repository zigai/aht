package launch

import (
	"fmt"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// TitleRequestFor returns the adapter's title recognizer. Title request rules
// in launch plans are independent of the launch paths and model endpoint.
func TitleRequestFor(id registry.Harness) (func(map[string]any) bool, error) {
	adapter, ok := catalog.Find(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, id)
	}

	planner, ok := adapter.(harness.Launchable)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, id)
	}

	plan, err := planner.LaunchPlan(harness.LaunchPaths{Root: "", Home: "", Work: ""},
		harness.LaunchModel{Endpoint: "", Model: "", APIKey: ""})
	if err != nil {
		return nil, fmt.Errorf("title request plan for %q: %w", id, err)
	}

	return plan.TitleRequest, nil
}
