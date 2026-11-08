package qwen

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (qwenHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source:    harness.DistributionNPM,
		Package:   "@qwen-code/qwen-code",
		Directory: "qwen",
	}
}
