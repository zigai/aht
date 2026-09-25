package amp

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (ampHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@ampcode/cli", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "amp", Family: "", PackageExtras: "", Install: nil}
}
