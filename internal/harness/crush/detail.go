package crush

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (crushHarness) ActivityDetail(string, registry.Activity, map[string]string) *registry.ActivityDetail {
	return nil
}

func (crushHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false}}
}
