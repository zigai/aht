package hermes

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (hermesHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "pre_approval_request" {
		return new(registry.DetailPermission)
	}
	return nil
}

func (hermesHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
