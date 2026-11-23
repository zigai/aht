package harness

import (
	"fmt"
	"slices"

	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Parse returns the supported harness identified by value. Harness names and
// documented aliases are matched without punctuation or case sensitivity.
func Parse(value string) (registry.Harness, error) {
	harnessID, err := adapters.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parsing harness: %w", err)
	}

	return harnessID, nil
}

func Supported() []registry.Harness {
	all := adapters.All()

	harnesses := make([]registry.Harness, 0, len(all))
	for _, adapter := range all {
		harnesses = append(harnesses, adapter.Definition().ID)
	}

	return harnesses
}

// FromCommand identifies a supported harness from an executable path or name.
func FromCommand(command string) (registry.Harness, bool) {
	return adapters.FromCommand(command)
}

func ProcessNames(harnessID registry.Harness) []string {
	return slices.Clone(adapters.ProcessNames(harnessID))
}
