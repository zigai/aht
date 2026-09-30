package droid

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (droidHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "Notification" && attributes["droid_notification_type"] == "permission_prompt" {
		return new(registry.ActivityDetailPermission)
	}
	return nil
}

func (droidHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
