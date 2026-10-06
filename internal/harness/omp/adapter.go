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
	integrationVersion     = 18
)

//go:embed assets/aht-state.ts.tmpl
var ompExtensionTemplate string

type ompHarness struct{ harness.BaseAdapter }

func New() ompHarness {
	return ompHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("omp"),
		Aliases:          []string{"ohmypi", "oh-my-pi", "oh_my_pi"},
		ProcessNames:     []string{"omp", "ohmypi", "oh-my-pi"},
		Env: harness.EnvKeys{
			SessionID:   nil,
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
		IntegrationSource:  ompIntegrationSourceID,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (ompHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := ompAgentDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(base, "extensions", ompExtensionName),
		Label:       "oh-my-pi extension",
		ConfigLabel: "oh-my-pi extension",
		Content: harness.RenderScriptTemplate(
			ompExtensionTemplate,
			ompIntegrationID,
			binary,
			ompIntegrationSourceID,
			integrationVersion,
		),
		JSONContent: nil,
	}}}}, nil
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

func (ompHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["omp_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "omp_reason", "omp_approval_reason", "source", "reason"))
}
