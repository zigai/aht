package omp

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (ompHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@oh-my-pi/pi-coding-agent",
		Directory: "omp",
		Family:    harness.FamilyTreeExtension,
	}
}
