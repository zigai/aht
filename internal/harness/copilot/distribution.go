package copilot

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (copilotHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@github/copilot", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "copilot", Family: "", PackageExtras: "", Install: nil}
}
