package pi

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (piHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@earendil-works/pi-coding-agent",
		Directory: "pi",
		Family:    harness.FamilyTreeExtension,
	}
}
