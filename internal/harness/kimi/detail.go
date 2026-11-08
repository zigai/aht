package kimi

import "github.com/zigai/aht/v2/pkg/registry"

func (kimiCodeHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	_ map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "PermissionRequest" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
