package claude

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (claudeHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@anthropic-ai/claude-code", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "claude", Family: "", PackageExtras: "", Install: nil}
}
