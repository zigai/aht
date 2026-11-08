package pi

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (piHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{
		Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true},
	}
}
