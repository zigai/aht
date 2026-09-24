package droid

import (
	"github.com/zigai/aht/internal/harness"
)

func (droidHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "droid", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "droid", Family: "", PackageExtras: "", Install: nil}
}
