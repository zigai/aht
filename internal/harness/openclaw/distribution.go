package openclaw

import (
	"github.com/zigai/aht/internal/harness"
)

func (openclawHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "openclaw", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "openclaw", Family: "", PackageExtras: "", Install: nil}
}
