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
	piIntegrationSourceID = "pi-extension"
	piSessionFlag         = "--session"
	integrationVersion    = 19
)

//go:embed assets/aht-state.ts.tmpl
var piExtensionTemplate string

type piHarness struct{ harness.BaseAdapter }

func New() piHarness {
	return piHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@earendil-works/pi-coding-agent",
			Directory: "pi",
			Family:    harness.FamilyTreeExtension,
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true},
		},
		ScreenManifest:   screenManifest,
		ExclusiveProcess: true,
		// The extension refreshes running with a heartbeat, and every other state
		// changes only through a new native event.
		RetainNativeActivity: true,
		ID:                   registry.Harness("pi"),
		ProcessNames:         []string{"pi"},
		Env: harness.EnvKeys{
			Config:      []string{"PI_CODING_AGENT_DIR"},
			SessionID:   []string{"PI_SESSION_ID"},
			SessionPath: []string{"PI_SESSION_PATH"},
			PID:         []string{"PI_PID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "pi_event",
		LifecycleSourceAttributes: []string{"pi_reason", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         piIntegrationSourceID,
		StateAuthority:            registry.AuthorityHook,
		ScreenFallback:            true,
	})}
}

func (adapter piHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := piAgentDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "extensions", piExtensionName),
				Label:       "pi extension",
				ConfigLabel: "pi extension",
				Content: harness.RenderScriptTemplate(
					piExtensionTemplate,
					binary,
					adapter.Definition(),
				),
			}},
		},
	}, nil
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

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".pi", "agent")
}
