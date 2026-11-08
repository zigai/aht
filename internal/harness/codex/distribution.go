package codex

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (codexHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@openai/codex",
		Directory: "codex",
	}
}
