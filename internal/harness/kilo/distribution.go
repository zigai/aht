package kilo

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (kiloHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@kilocode/cli", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "kilo", Family: "part-plugin", PackageExtras: "", Install: nil}
}
