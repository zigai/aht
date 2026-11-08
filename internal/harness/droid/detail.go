package droid

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (droidHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "Notification" &&
		attributes["droid_notification_type"] == "permission_prompt" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
