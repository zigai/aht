package claude

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (claudeHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@anthropic-ai/claude-code",
		Directory: "claude",
	}
}
