package goose

import (
	"path/filepath"

	"github.com/zigai/aht/internal/harness"
)

func (gooseHarness) Distribution() harness.Distribution {
	return harness.Distribution{Source: "github", Package: "", Repo: "aaif-goose/goose", Asset: "download_cli.sh", URL: "", MaxVersion: "", Directory: "goose", Family: "", PackageExtras: "", Install: distributionInstall}
}

func distributionInstall(version, work, bin string) []harness.DistributionCommand {
	asset := filepath.Join(work, "download_cli.sh")
	return []harness.DistributionCommand{harness.DownloadCommand("https://github.com/aaif-goose/goose/releases/download/"+version+"/download_cli.sh", asset), {Name: "bash", Args: []string{asset}, Env: []string{"GOOSE_VERSION=" + version, "GOOSE_BIN_DIR=" + bin, "CONFIGURE=false"}}}
}
