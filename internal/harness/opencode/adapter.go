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
	integrationVersion        = 12
	opencodePluginName        = "aht-state.ts"
	opencodeIntegrationID     = "opencode"
	opencodeIntegrationSource = "opencode-plugin"
	opencodeSessionFlag       = "--session"
)

//go:embed assets/aht-state.ts.tmpl
var opencodePluginTemplate string

type opencodeHarness struct{ harness.BaseAdapter }

func New() opencodeHarness {
	return opencodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("opencode"),
		Aliases:          []string{"open-code", "open_code"},
		ProcessNames:     []string{"opencode", "open-code"},
		Env: harness.EnvKeys{
			SessionID:   []string{"OPENCODE_SESSION_ID"},
			SessionPath: []string{"OPENCODE_SESSION_PATH"},
			ProjectRoot: nil,
			PID:         []string{"OPENCODE_PID"},
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
			NativeCatalog:     true,
			ProcessIdentity:   false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  opencodeIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (opencodeHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{
		harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
			Path:        filepath.Join(opencodeConfigDir(), "plugins", opencodePluginName),
			Label:       "opencode plugin",
			ConfigLabel: "opencode plugin",
			Content: harness.RenderScriptTemplate(
				opencodePluginTemplate,
				opencodeIntegrationID,
				binary,
				opencodeIntegrationSource,
				integrationVersion,
			),
			JSONContent: nil,
		}},
		harness.ShimAction{},
	}}
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

	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "opencode")
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "opencode")
	}

	return filepath.Join(".config", "opencode")
}
