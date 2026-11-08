package catalog

import (
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

type Rules struct{}

func (Rules) SamePane(a registry.Location, b registry.Location) bool {
	driver, ok := drivers.Find(a.Kind)
	return ok && driver.SamePane(a, b)
}

func (Rules) Known(id registry.Harness) bool {
	_, ok := Find(id)
	return ok
}

func (Rules) Policy(id registry.Harness) registry.Policy {
	adapter, ok := Find(id)
	if !ok {
		return registry.Policy{
			ExclusiveProcess:     false,
			Authority:            registry.AuthorityHook,
			ScreenFallback:       false,
			RetainNativeActivity: false,
			Reporter:             "",
			CatalogCreates:       false,
			Details: registry.DetailCapabilities{
				Native: registry.DetailSupport{
					Permission: false,
					Question:   false,
					UsageLimit: false,
				},
				Screen: registry.DetailSupport{
					Permission: false, Question: false, UsageLimit: false,
				},
			},
		}
	}

	definition := adapter.Definition()

	return registry.Policy{
		Details:              DetailCapabilitiesFor(id),
		ExclusiveProcess:     definition.ExclusiveProcess,
		Authority:            definition.StateAuthority,
		ScreenFallback:       definition.ScreenFallback,
		RetainNativeActivity: definition.RetainNativeActivity,
		Reporter:             definition.IntegrationSource,
		CatalogCreates:       definition.CatalogCreates,
	}
}

func Harnesses() []registry.Harness {
	result := make([]registry.Harness, 0, len(adapters))
	for _, adapter := range adapters {
		result = append(result, adapter.Definition().ID)
	}

	return result
}
