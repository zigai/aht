package agy

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func distributionInstall(version string, work string, bin string) []harness.DistributionCommand {
	asset := filepath.Join(work, "agy_cli_linux_x64.tar.gz")

	return []harness.DistributionCommand{
		harness.DownloadCommand(
			"https://github.com/google-antigravity/antigravity-cli/"+
				"releases/download/"+version+"/agy_cli_linux_x64.tar.gz",
			asset,
		),
		{Name: "tar", Args: []string{"-xzf", asset, "-C", work, "antigravity"}, Env: nil},
		{
			Name: "install",
			Args: []string{
				"-m",
				"0755",
				filepath.Join(work, "antigravity"),
				filepath.Join(bin, "agy"),
			},
			Env: nil,
		},
	}
}
