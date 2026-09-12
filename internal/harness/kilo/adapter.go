package kilo

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	integrationVersion = 8
	kiloCommand        = "kilo"
	kiloSessionFlag    = "--session"
)

const (
	kiloPluginName        = "aht-state.ts"
	kiloIntegrationID     = "kilo"
	kiloIntegrationSource = "kilo-plugin"
)

//go:embed assets/aht-state.ts.tmpl
var kiloPluginTemplate string

type kiloHarness struct{ harness.BaseAdapter }

func New() kiloHarness {
	return kiloHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessKilo,
		Aliases:      []string{"kilocode", "kilo-code", "kilo_code"},
		ProcessNames: []string{"kilo", "kilocode", "kilo-code", "kilo_code"},
		Env: harness.EnvKeys{
			SessionID:   []string{"KILO_SESSION_ID"},
			SessionPath: []string{"KILO_SESSION_PATH"},
			ProjectRoot: []string{"KILO_PROJECT_ROOT"},
			PID:         []string{"KILO_PID"},
			Event:       []string{"KILO_EVENT"},
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

func (kiloHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(kiloConfigDir(), "plugin", kiloPluginName),
		Label:       "kilo plugin",
		ConfigLabel: "kilo plugin",
		Content: harness.RenderScriptTemplate(
			kiloPluginTemplate,
			kiloIntegrationID,
			binary,
			kiloIntegrationSource,
			integrationVersion,
		),
		JSONContent: nil,
	}}}}
}

func (kiloHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{kiloCommand, kiloSessionFlag, sessionID}
}

func kiloConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("KILO_CONFIG_DIR")); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "kilo")
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "kilo")
	}

	return filepath.Join(".config", "kilo")
}
