package hermes

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (hermesHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "pre_approval_request" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
