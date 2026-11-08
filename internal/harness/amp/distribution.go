package amp

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (ampHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@ampcode/cli",
		Directory: "amp",
	}
}
