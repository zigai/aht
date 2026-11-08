package catalog

import (
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func DistributionFor(id registry.Harness) harness.Distribution {
	if adapter, ok := Find(id); ok {
		return adapter.Definition().Distribution
	}

	return harness.Distribution{}
}
