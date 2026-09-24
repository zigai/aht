package opencode

import (
	"github.com/zigai/aht/internal/harness"
)

func (opencodeHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "opencode-ai", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "opencode", Family: "part-plugin", PackageExtras: "", Install: nil}
}
