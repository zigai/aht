package omp

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (ompHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@oh-my-pi/pi-coding-agent", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "omp", Family: "tree-extension", PackageExtras: "", Install: nil}
}
