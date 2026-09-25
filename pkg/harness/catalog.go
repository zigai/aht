package harness

import (
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Parse returns the supported harness identified by value. Harness names and
// documented aliases are matched without punctuation or case sensitivity.
func Parse(value string) (registry.Harness, error) {
	harnessID, err := catalog.Normalize(value)
	if err != nil {
		return "", fmt.Errorf("parsing harness: %w", err)
	}
	return harnessID, nil
}

// Supported returns every harness recognized by this AHT version.
func Supported() []registry.Harness {
	adapters := catalog.All()
	harnesses := make([]registry.Harness, 0, len(adapters))
	for _, adapter := range adapters {
		harnesses = append(harnesses, adapter.Definition().ID)
	}
	return harnesses
}

// FromCommand identifies a supported harness from an executable path or name.
func FromCommand(command string) (registry.Harness, bool) {
	return catalog.FromCommand(command)
}

// ProcessNames returns executable names associated with harness.
func ProcessNames(harness registry.Harness) []string {
	return catalog.ProcessNames(harness)
}
