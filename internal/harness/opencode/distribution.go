package opencode

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (opencodeHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@opencode/cli",
		Directory: "opencode",
		Family:    harness.FamilyPartPlugin,
	}
}
