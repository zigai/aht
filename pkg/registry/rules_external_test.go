package registry_test

import (
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

type behaviorRules struct{}

func (behaviorRules) SamePane(a registry.Location, b registry.Location) bool {
	driver, ok := drivers.Find(a.Kind)
	return ok && driver.SamePane(a, b)
}

func (behaviorRules) Known(id registry.Harness) bool { return id != "" }
func (behaviorRules) Policy(id registry.Harness) registry.Policy {
	authority := registry.AuthorityHook
	if id == "catalog" {
		authority = registry.AuthorityScreen
	}

	return registry.Policy{
		ExclusiveProcess: true,
		Authority:        authority,
		ScreenFallback:   true,
		Reporter:         string(id) + "-extension",
		CatalogCreates:   id == "catalog",
	}
}
