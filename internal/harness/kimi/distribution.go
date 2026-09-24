package kimi

import (
	"github.com/zigai/aht/internal/harness"
)

func (kimiCodeHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "pypi", Package: "kimi-cli", Repo: "", Asset: "", URL: "", MaxVersion: "1.51.0", Directory: "kimi", Family: "", PackageExtras: "", Install: nil}
}
