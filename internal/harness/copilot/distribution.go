package copilot

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (copilotHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@github/copilot",
		Directory: "copilot",
	}
}
