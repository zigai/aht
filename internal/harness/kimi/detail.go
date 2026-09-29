package kimi

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (kimiCodeHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	return nil
}

func (kimiCodeHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}

func (kimiCodeHarness) DetailReporters() []string { return []string{"kimi-wire"} }
