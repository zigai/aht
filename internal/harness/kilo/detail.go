package kilo

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (kiloHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "permission.asked" {
		return new(registry.ActivityDetailPermission)
	}
	return nil
}

func (kiloHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
