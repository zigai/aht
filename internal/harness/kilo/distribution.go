package kilo

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (kiloHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@kilocode/cli",
		Directory: "kilo",
		Family:    harness.FamilyPartPlugin,
	}
}
