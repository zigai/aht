package openclaw

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (openclawHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "openclaw",
		Directory: "openclaw",
	}
}
