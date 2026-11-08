package harness

import (
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Parse returns the supported harness identified by value. Harness names and
// documented aliases are matched without punctuation or case sensitivity.
func Parse(value string) (registry.Harness, error) {
	harnessID, err := catalog.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parsing harness: %w", err)
	}

	return harnessID, nil
}
