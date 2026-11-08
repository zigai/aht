package opencode

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (opencodeHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "question.asked" {
		return new(registry.ActivityDetailQuestion)
	}

	if activity == registry.ActivityWaiting && event == "permission.asked" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
