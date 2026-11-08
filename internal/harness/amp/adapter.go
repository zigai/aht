package amp

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	ampPluginName        = "aht-state.ts"
	ampIntegrationSource = "amp-plugin"
	integrationVersion   = 6
)

//go:embed assets/aht-state.ts.tmpl
var ampPluginTemplate string

type ampHarness struct{ harness.BaseAdapter }

func New() ampHarness {
	return ampHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@ampcode/cli",
			Directory: "amp",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("amp"),
		Aliases:          []string{"ampcode", "amp-code", "amp_code"},
		ProcessNames:     []string{"amp"},
		Env: harness.EnvKeys{
			Config:    []string{"AMP_DATA_DIR"},
			SessionID: []string{"AMP_THREAD_ID", "AMP_SESSION_ID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  ampIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter ampHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := ampConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "plugins", ampPluginName),
				Label:       "amp plugin",
				ConfigLabel: "amp plugin",
				Content: harness.RenderScriptTemplate(
					ampPluginTemplate,
					binary,
					adapter.Definition(),
				),
			}},
		},
	}, nil
}

func (ampHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"amp", "threads", "continue", sessionID}
}

func ampConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "amp")
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".config", "amp")
}
