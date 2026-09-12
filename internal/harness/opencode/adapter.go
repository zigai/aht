package opencode

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	integrationVersion        = 9
	openCodePluginName        = "aht-state.ts"
	openCodeIntegrationID     = "opencode"
	openCodeIntegrationSource = "opencode-plugin"
	openCodeSessionFlag       = "--session"
)

//go:embed assets/aht-state.ts.tmpl
var openCodePluginTemplate string

type openCodeHarness struct{ harness.BaseAdapter }

func New() openCodeHarness {
	return openCodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessOpenCode,
		Aliases:      []string{"open-code", "open_code"},
		ProcessNames: []string{"opencode", "open-code"},
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
	})}
}

func (openCodeHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{
		harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
			Path:        filepath.Join(openCodeConfigDir(), "plugins", openCodePluginName),
			Label:       "opencode plugin",
			ConfigLabel: "opencode plugin",
			Content: harness.RenderScriptTemplate(
				openCodePluginTemplate,
				openCodeIntegrationID,
				binary,
				openCodeIntegrationSource,
				integrationVersion,
			),
			JSONContent: nil,
		}},
		harness.ShimAction{},
	}}
}

func (openCodeHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"opencode", openCodeSessionFlag, sessionID}
}

func openCodeConfigDir() string {
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
