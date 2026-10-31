package prime

import "github.com/zigai/aht/v2/pkg/registry"

func (primeHarness) ActivityDetail(
	_ string,
	activity registry.Activity,
	attributes map[string]string,
) *registry.ActivityDetail {
	if activity == registry.ActivityFailed && attributes["prime_quota_parked"] == "true" {
		return new(registry.ActivityDetailUsageLimit)
	}

	return nil
}

func (primeHarness) DetailCapabilities() registry.DetailCapabilities {
	return registry.DetailCapabilities{
		Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: true},
		Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
	}
}
