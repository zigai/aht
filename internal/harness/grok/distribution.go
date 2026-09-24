package grok

import (
	"path/filepath"

	"github.com/zigai/aht/internal/harness"
)

func (grokHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "channel", Package: "", Repo: "", Asset: "", URL: "https://x.ai/cli/stable", MaxVersion: "", Directory: "grok", Family: "", PackageExtras: "", Install: distributionInstall}
}

func distributionInstall(version, work, bin string) []harness.DistributionCommand {
	script := filepath.Join(work, "install-grok.sh")
	return []harness.DistributionCommand{harness.DownloadCommand("https://x.ai/cli/install.sh", script), {Name: "bash", Args: []string{script, version}, Env: []string{"GROK_BIN_DIR=" + bin}}}
}
