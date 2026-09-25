package cursor

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (cursorHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "weekly", Package: "", Repo: "", Asset: "", URL: "", MaxVersion: "", Directory: "cursor", Family: "", PackageExtras: "", Install: distributionInstall}
}

func distributionInstall(_ string, work, _ string) []harness.DistributionCommand {
	script := filepath.Join(work, "install-cursor.sh")
	return []harness.DistributionCommand{harness.DownloadCommand("https://cursor.com/install", script), {Name: "bash", Args: []string{script}, Env: nil}}
}
