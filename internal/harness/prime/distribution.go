package prime

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

const primeInstallerURL = "https://app.primeintellect.ai/prime-agent/install.sh"

func distributionInstall(version string, work string, bin string) []harness.DistributionCommand {
	installer := filepath.Join(work, "prime-agent-install.sh")

	return []harness.DistributionCommand{
		harness.DownloadCommand(primeInstallerURL, installer),
		{
			Name: "sh",
			Args: []string{installer},
			Env: []string{
				"PRIME_AGENT_VERSION=" + version,
				"PRIME_AGENT_RUST_PREFIX=" + filepath.Dir(bin),
			},
		},
	}
}
