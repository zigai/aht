package launch

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/registry"
)

// TitleRequestFor returns the harness's title recognizer. Title request rules
// in launch plans are independent of the launch paths and model endpoint.
func TitleRequestFor(id registry.Harness) (func(map[string]any) bool, error) {
	planner, ok := plans[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupported, id)
	}

	plan, err := planner(Paths{Root: "", Home: "", Work: ""},
		Model{Endpoint: "", Model: "", APIKey: ""})
	if err != nil {
		return nil, fmt.Errorf("title request plan for %q: %w", id, err)
	}

	return plan.TitleRequest, nil
}
