package agy

import (
	"path/filepath"

	"github.com/zigai/aht/internal/harness"
)

func (agyHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "github", Package: "", Repo: "google-antigravity/antigravity-cli", Asset: "agy_cli_linux_x64.tar.gz", URL: "", MaxVersion: "", Directory: "agy", Family: "", PackageExtras: "", Install: distributionInstall}
}

func distributionInstall(version, work, bin string) []harness.DistributionCommand {
	asset := filepath.Join(work, "agy_cli_linux_x64.tar.gz")
	return []harness.DistributionCommand{harness.DownloadCommand("https://github.com/google-antigravity/antigravity-cli/releases/download/"+version+"/agy_cli_linux_x64.tar.gz", asset), {Name: "tar", Args: []string{"-xzf", asset, "-C", work, "antigravity"}, Env: nil}, {Name: "install", Args: []string{"-m", "0755", filepath.Join(work, "antigravity"), filepath.Join(bin, "agy")}, Env: nil}}
}
