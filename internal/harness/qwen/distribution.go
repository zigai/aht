package qwen

import (
	"github.com/zigai/aht/v2/internal/harness"
)

func (qwenHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "npm", Package: "@qwen-code/qwen-code", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "qwen", Family: "", PackageExtras: "", Install: nil}
}
