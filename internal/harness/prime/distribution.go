package prime

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

const primeInstallerURL = "https://app.primeintellect.ai/prime-agent/install.sh"

func (primeHarness) Distribution() harness.Distribution {
	return harness.Distribution{
		Source: "channel", Package: "", Repo: "", Asset: "",
		URL: "https://pub-728493de92a943e2a9b2d17b4719f318.r2.dev/stable", MaxVersion: "",
		Directory: "prime", Family: "", PackageExtras: "", Install: distributionInstall,
	}
}

func distributionInstall(version, work, bin string) []harness.DistributionCommand {
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
