package omp

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (ompHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	return nil
}

func (ompHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false}, Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true}}
}
