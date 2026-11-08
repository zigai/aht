package cline

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (clineHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "cline",
		Directory: "cline",
	}
}
