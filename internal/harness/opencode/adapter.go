package opencode

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	integrationVersion        = 16
	opencodePluginName        = "aht-state.ts"
	opencodeIntegrationSource = "opencode-plugin"
	opencodeSessionFlag       = "--session"
)

//go:embed assets/aht-state.ts.tmpl
var opencodePluginTemplate string

type opencodeHarness struct{ harness.BaseAdapter }

func New() opencodeHarness {
	return opencodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@opencode/cli",
			Directory: "opencode",
			Family:    harness.FamilyPartPlugin,
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false},
		},
		ScreenManifest:   screenManifest,
		ExclusiveProcess: true,
		ID:               registry.Harness("opencode"),
		Aliases:          []string{"open-code", "open_code"},
		ProcessNames:     []string{"opencode", "open-code"},
		Env: harness.EnvKeys{
			Config:      []string{"OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "OPENCODE_DB"},
			SessionID:   []string{"OPENCODE_SESSION_ID"},
			SessionPath: []string{"OPENCODE_SESSION_PATH"},
			PID:         []string{"OPENCODE_PID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  opencodeIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (adapter opencodeHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := opencodeConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{
		harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
			Path:        filepath.Join(base, "plugins", opencodePluginName),
			Label:       "opencode plugin",
			ConfigLabel: "opencode plugin",
			Content: harness.RenderScriptTemplate(
				opencodePluginTemplate,
				binary,
				adapter.Definition(),
			),
		}},
		harness.ShimAction{},
	}}, nil
}

func (opencodeHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"opencode", opencodeSessionFlag, sessionID}
}

func opencodeConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); value != "" {
		return filepath.Dir(value)
	}

	return opencodeUserConfigDir()
}

func opencodeUserConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "opencode")
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".config", "opencode")
}
