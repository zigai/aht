package amp

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (ampHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "thread.state" &&
		attributes["amp_thread_state"] == "awaiting-approval" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
