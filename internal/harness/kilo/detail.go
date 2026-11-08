package kilo

import (
	"github.com/zigai/aht/v2/pkg/registry"
)

func (kiloHarness) ActivityDetail(
	event string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityWaiting && event == "permission.asked" {
		return new(registry.ActivityDetailPermission)
	}

	return nil
}
