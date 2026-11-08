package grok

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func distributionInstall(version string, work string, bin string) []harness.DistributionCommand {
	script := filepath.Join(work, "install-grok.sh")

	return []harness.DistributionCommand{
		harness.DownloadCommand("https://x.ai/cli/install.sh", script),
		{Name: "bash", Args: []string{script, version}, Env: []string{"GROK_BIN_DIR=" + bin}},
	}
}
