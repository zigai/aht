package catalog

import "github.com/zigai/aht/v2/pkg/registry"

type Rules struct{}

type detailReporters interface {
	DetailReporters() []string
}

type nativeActivityRetainer interface {
	RetainNativeActivity() bool
}

func (Rules) Known(id registry.Harness) bool {
	_, ok := Find(id)
	return ok
}

func (Rules) Policy(id registry.Harness) registry.Policy {
	adapter, ok := Find(id)
	if !ok {
		return registry.Policy{
			ExclusiveProcess: false, Authority: registry.AuthorityHook, ScreenFallback: false, RetainNativeActivity: false, Reporter: "", CatalogCreates: false, DetailReporters: nil, Details: registry.DetailCapabilities{Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false}, Screen: registry.DetailSupport{
				Permission: false, Question: false, UsageLimit: false,
			}},
		}
	}
	definition := adapter.Definition()
	retainNativeActivity := false
	if retainer, ok := adapter.(nativeActivityRetainer); ok {
		retainNativeActivity = retainer.RetainNativeActivity()
	}
	var reporters []string
	if provider, ok := adapter.(detailReporters); ok {
		reporters = provider.DetailReporters()
	}
	return registry.Policy{
		DetailReporters:      reporters,
		Details:              DetailCapabilitiesFor(id),
		ExclusiveProcess:     definition.ExclusiveProcess,
		Authority:            definition.StateAuthority,
		ScreenFallback:       definition.ScreenFallback,
		RetainNativeActivity: retainNativeActivity,
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
