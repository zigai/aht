package codex

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (codexHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PermissionRequest" {
		return new(registry.DetailPermission)
	}
	return nil
}

func (codexHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: true, Question: false, UsageLimit: true}}
}
