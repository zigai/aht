package agy

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (agyHarness) ActivityDetail(event string, activity registry.Activity, attributes map[string]string) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PreToolUse" {
		switch attributes["agy_tool_name"] {
		case "ask_permission":
			return new(registry.DetailPermission)
		case "ask_question":
			return new(registry.DetailQuestion)
		}
	}
	return nil
}

func (agyHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false}, Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}}
}
