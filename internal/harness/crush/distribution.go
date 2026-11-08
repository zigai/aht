package crush

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (crushHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@charmland/crush",
		Directory: "crush",
	}
}
