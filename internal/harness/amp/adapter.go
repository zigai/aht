package amp

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	ampPluginName        = "aht-state.ts"
	ampIntegrationID     = "amp"
	ampIntegrationSource = "amp-plugin"
	integrationVersion   = 2
)

//go:embed assets/aht-state.ts.tmpl
var ampPluginTemplate string

type ampHarness struct{ harness.BaseAdapter }

func New() ampHarness {
	return ampHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessAmp,
		Aliases:      []string{"ampcode", "amp-code", "amp_code"},
		ProcessNames: []string{"amp"},
		Env: harness.EnvKeys{
			SessionID:   []string{"AMP_THREAD_ID", "AMP_SESSION_ID"},
			SessionPath: nil,
			ProjectRoot: nil,
			PID:         nil,
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
		IntegrationSource:  ampIntegrationSource,
		StateAuthority:     harness.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (ampHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(ampConfigDir(), "plugins", ampPluginName),
		Label:       "amp plugin",
		ConfigLabel: "amp plugin",
		Content: harness.RenderScriptTemplate(
			ampPluginTemplate,
			ampIntegrationID,
			binary,
			ampIntegrationSource,
			integrationVersion,
		),
		JSONContent: nil,
	}}}}
}

func (ampHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"amp", "threads", "continue", sessionID}
}

func ampConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("AMP_CONFIG_DIR")); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "amp")
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config", "amp")
	}

	return filepath.Join(".config", "amp")
}
