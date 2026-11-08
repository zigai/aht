package omp

import (
	_ "embed"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	ompExtensionName       = "aht-state.ts"
	ompIntegrationID       = "omp"
	ompIntegrationSourceID = "omp-extension"
	ompSessionFlag         = "--session"
	integrationVersion     = 19
)

//go:embed assets/aht-state.ts.tmpl
var ompExtensionTemplate string

type ompHarness struct{ harness.BaseAdapter }

func New() ompHarness {
	return ompHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@oh-my-pi/pi-coding-agent",
			Directory: "omp",
			Family:    harness.FamilyTreeExtension,
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true},
		},
		ScreenManifest: screenManifest,
		Env: harness.EnvKeys{
			Config: []string{"PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "OMP_PROFILE", "PI_PROFILE"},
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("omp"),
		Aliases:          []string{"ohmypi", "oh-my-pi", "oh_my_pi"},
		ProcessNames:     []string{"omp", "ohmypi", "oh-my-pi"},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute: "omp_event",
		LifecycleSourceAttributes: []string{
			"omp_reason",
			"omp_approval_reason",
			"source",
			"reason",
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  ompIntegrationSourceID,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (adapter ompHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := ompAgentDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "extensions", ompExtensionName),
				Label:       "oh-my-pi extension",
				ConfigLabel: "oh-my-pi extension",
				Content: harness.RenderScriptTemplate(
					ompExtensionTemplate,
					binary,
					adapter.Definition(),
				),
			}},
		},
	}, nil
}

func (ompHarness) ResumeCommand(sessionID string, sessionPath string) []string {
	if sessionPath != "" {
		return []string{ompIntegrationID, ompSessionFlag, sessionPath}
	}

	if sessionID != "" {
		return []string{ompIntegrationID, ompSessionFlag, sessionID}
	}

	return nil
}

func (ompHarness) ObservableProcess(process processinfo.Process) bool {
	for _, arg := range process.Args {
		if strings.HasPrefix(filepath.Base(arg), "__omp_worker_") {
			return false
		}
	}

	return true
}
