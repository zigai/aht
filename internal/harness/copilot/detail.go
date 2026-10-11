package copilot

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (copilotHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity != registry.ActivityWaiting {
		return nil
	}
	if event == "permissionRequest" || event == "notification" && attributes["copilot_notification_type"] == "permission_prompt" {
		return new(registry.ActivityDetailPermission)
	}
	return nil
}

func (copilotHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
