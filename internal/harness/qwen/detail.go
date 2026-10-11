package qwen

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (qwenHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityFailed && event == "StopFailure" {
		switch attributes["qwen_error_type"] {
		case "rate_limit", "billing_error":
			return new(registry.ActivityDetailUsageLimit)
		}
	}
	if activity == registry.ActivityWaiting && event == "Notification" && attributes["qwen_notification_type"] == "permission_prompt" {
		return new(registry.ActivityDetailPermission)
	}
	return nil
}

func (qwenHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: true}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
