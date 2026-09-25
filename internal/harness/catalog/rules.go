package catalog

import "github.com/zigai/aht/v2/pkg/registry"

type Rules struct{}

func (Rules) Known(id registry.Harness) bool {
	_, ok := Find(id)
	return ok
}

func (Rules) Policy(id registry.Harness) registry.Policy {
	adapter, ok := Find(id)
	if !ok {
		return registry.Policy{ExclusiveProcess: false, Authority: registry.AuthorityHook, ScreenFallback: false, Reporter: "", CatalogCreates: false}
	}
	definition := adapter.Definition()
	return registry.Policy{
		ExclusiveProcess: definition.ExclusiveProcess,
		Authority:        definition.StateAuthority,
		ScreenFallback:   definition.ScreenFallback,
		Reporter:         definition.IntegrationSource,
		CatalogCreates:   definition.CatalogCreates,
	}
}

func Harnesses() []registry.Harness {
	result := make([]registry.Harness, 0, len(adapters))
	for _, adapter := range adapters {
		result = append(result, adapter.Definition().ID)
	}
	return result
}
