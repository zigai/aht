package agy

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (agyHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PreToolUse" {
		switch attributes["agy_tool_name"] {
		case "ask_permission":
			return new(registry.ActivityDetailPermission)
		case "ask_question":
			return new(registry.ActivityDetailQuestion)
		}
	}

	return nil
}
