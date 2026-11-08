package hermes

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (hermesHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:        harness.DistributionPyPI,
		Package:       "hermes-agent",
		Directory:     "hermes",
		PackageExtras: "[acp]",
	}
}
