package pi

import (
	"github.com/zigai/aht/internal/harness"
)

func (piHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@earendil-works/pi-coding-agent", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "pi", Family: "tree-extension", PackageExtras: "", Install: nil}
}
