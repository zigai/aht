package pi

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (piHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity != registry.ActivityWaiting || event != "ui_prompt_start" {
		return nil
	}

	switch attributes["pi_prompt_kind"] {
	case "confirm":
		return new(registry.ActivityDetailPermission)
	case "select", "input", "editor":
		return new(registry.ActivityDetailQuestion)
	}

	return nil
}
