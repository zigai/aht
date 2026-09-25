package pi

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	piExtensionName       = "aht-state.ts"
	piIntegrationID       = "pi"
	piIntegrationSourceID = "pi-extension"
	piSessionFlag         = "--session"
	integrationVersion    = 16
)

//go:embed assets/aht-state.ts.tmpl
var piExtensionTemplate string

type piHarness struct{ harness.BaseAdapter }

func New() piHarness {
	return piHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("pi"),
		Aliases:          nil,
		ProcessNames:     []string{"pi"},
		Env: harness.EnvKeys{
			SessionID:   []string{"PI_SESSION_ID"},
			SessionPath: []string{"PI_SESSION_PATH"},
			ProjectRoot: nil,
			PID:         []string{"PI_PID"},
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
		IntegrationSource:  piIntegrationSourceID,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (piHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(piAgentDir(), "extensions", piExtensionName),
		Label:       "pi extension",
		ConfigLabel: "pi extension",
		Content: harness.RenderScriptTemplate(
			piExtensionTemplate,
			piIntegrationID,
			binary,
			piIntegrationSourceID,
			integrationVersion,
		),
		JSONContent: nil,
	}}}}
}

func (piHarness) ResumeCommand(sessionID string, sessionPath string) []string {
	if sessionPath != "" {
		return []string{"pi", piSessionFlag, sessionPath}
	}
	if sessionID != "" {
		return []string{"pi", piSessionFlag, sessionID}
	}

	return nil
}

func piAgentDir() string {
	if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
		return value
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".pi", "agent")
	}

	return filepath.Join(".pi", "agent")
}

func (piHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["pi_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "pi_reason", "source", "reason"))
}
