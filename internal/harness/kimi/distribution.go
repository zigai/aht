package kimi

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (kimiCodeHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@moonshot-ai/kimi-code",
		Directory: "kimi",
	}
}
