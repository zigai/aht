package registry_test

import "github.com/zigai/aht/v2/pkg/registry"

type behaviorRules struct{}

func (behaviorRules) Known(id registry.Harness) bool { return id != "" }
func (behaviorRules) Policy(id registry.Harness) registry.Policy {
	authority := registry.AuthorityHook
	if id == "claude" {
		authority = registry.AuthorityScreen
	}
	return registry.Policy{ExclusiveProcess: true, Authority: authority, ScreenFallback: true, Reporter: string(id) + "-extension", CatalogCreates: id == "claude"}
}
