package codex

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (codexHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PermissionRequest" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
