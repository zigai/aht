package catalog

import (
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func DistributionFor(id registry.Harness) harness.Distribution {
	if adapter, ok := Find(id); ok {
		if provider, ok := adapter.(interface{ Distribution() harness.Distribution }); ok {
			return provider.Distribution()
		}
	}
	return harness.Distribution{Source: "", Package: "", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "", Family: "", PackageExtras: "", Install: nil}
}
