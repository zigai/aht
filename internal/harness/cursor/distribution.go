package cursor

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func distributionInstall(_ string, work string, bin string) []harness.DistributionCommand {
	script := filepath.Join(work, "install-cursor.sh")

	return []harness.DistributionCommand{
		harness.DownloadCommand("https://cursor.com/install", script),
		{Name: "bash", Args: []string{script}, Env: nil},
		{Name: "sh", Args: []string{
			"-c",
			`if [ "$1" != "$HOME/.local/bin" ]; then mkdir -p "$1" && ` +
				`ln -sf "$HOME/.local/bin/cursor-agent" "$1/cursor-agent"; fi`,
			"cursor-install",
			bin,
		}, Env: nil},
	}
}
