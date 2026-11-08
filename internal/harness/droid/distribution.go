package droid

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (droidHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "droid",
		Directory: "droid",
	}
}
