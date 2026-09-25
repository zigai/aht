package codex

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (codexHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@openai/codex", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "codex", Family: "", PackageExtras: "", Install: nil}
}
