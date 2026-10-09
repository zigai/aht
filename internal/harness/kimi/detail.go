package kimi

import "github.com/zigai/aht/v2/pkg/registry"

func (kimiCodeHarness) ActivityDetail(event string, activity registry.Activity, _ map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PermissionRequest" {
		return new(registry.ActivityDetailPermission)
	}
	return nil
}

func (kimiCodeHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
