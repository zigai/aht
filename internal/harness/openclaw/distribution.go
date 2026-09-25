package openclaw

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (openclawHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "openclaw", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "openclaw", Family: "", PackageExtras: "", Install: nil}
}
