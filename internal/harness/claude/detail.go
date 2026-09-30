package claude

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (claudeHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityFailed && event == "StopFailure" {
		switch attributes["claude_error_type"] {
		case "rate_limit", "billing_error":
			return new(registry.DetailUsageLimit)
		}
	}
	return hookActivityDetail(event, activity, attributes["claude_notification_type"])
}

func (claudeHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true}, Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true}}
}

func hookActivityDetail(event string, activity registry.Activity, notification string) *registry.ActivityDetail {
	if activity != registry.ActivityWaiting {
		return nil
	}
	switch event {
	case "PermissionRequest":
		return new(registry.DetailPermission)
	case "Elicitation":
		return new(registry.DetailQuestion)
	case "Notification":
		switch notification {
		case "permission_prompt":
			return new(registry.DetailPermission)
		case "elicitation_dialog", "elicitation_url_dialog":
			return new(registry.DetailQuestion)
		}
	}
	return nil
}
