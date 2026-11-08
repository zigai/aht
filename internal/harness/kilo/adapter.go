package kilo

import (
	_ "embed"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	integrationVersion = 13
	kiloCommand        = "kilo"
	kiloSessionFlag    = "--session"
)

const (
	kiloPluginName        = "aht-state.ts"
	kiloIntegrationSource = "kilo-plugin"
)

//go:embed assets/aht-state.ts.tmpl
var kiloPluginTemplate string

type kiloHarness struct{ harness.BaseAdapter }

func New() kiloHarness {
	return kiloHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@kilocode/cli",
			Directory: "kilo",
			Family:    harness.FamilyPartPlugin,
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("kilo"),
		Aliases:          []string{"kilocode", "kilo-code", "kilo_code"},
		ProcessNames:     []string{"kilo", "kilocode", "kilo-code", "kilo_code"},
		Env: harness.EnvKeys{
			Config:      []string{"KILO_CONFIG_DIR", "KILO_CONFIG", "KILO_DB"},
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
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  kiloIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter kiloHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := kiloPaths(harness.HomeDir()).config
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "plugin", kiloPluginName),
				Label:       "kilo plugin",
				ConfigLabel: "kilo plugin",
				Content: harness.RenderScriptTemplate(
					kiloPluginTemplate,
					binary,
					adapter.Definition(),
				),
			}},
		},
	}, nil
}

func (kiloHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{kiloCommand, kiloSessionFlag, sessionID}
}
