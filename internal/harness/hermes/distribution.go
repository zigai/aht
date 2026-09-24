package hermes

import (
	"github.com/zigai/aht/internal/harness"
)

func (hermesHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "pypi", Package: "hermes-agent", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "hermes", Family: "", PackageExtras: "[acp]", Install: nil}
}
