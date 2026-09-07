package omp

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	ompExtensionName       = "aht-state.ts"
	ompIntegrationID       = "omp"
	ompIntegrationSourceID = "omp-extension"
	ompSessionFlag         = "--session"
	integrationVersion     = 14
)

//go:embed assets/aht-state.ts.tmpl
var ompExtensionTemplate string

type ompHarness struct{ harness.BaseAdapter }

func New() ompHarness {
	return ompHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessOmp,
		Aliases:      []string{"ohmypi", "oh-my-pi", "oh_my_pi"},
		ProcessNames: []string{"omp", "ohmypi", "oh-my-pi"},
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
	})}
}

func (ompHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(ompAgentDir(), "extensions", ompExtensionName),
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
	}}}}
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

func ompAgentDir() string {
	if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
		return value
	}

	configRoot := strings.TrimSpace(os.Getenv("PI_CONFIG_DIR"))
	if configRoot == "" {
		configRoot = ".omp"
	}
	if !filepath.IsAbs(configRoot) {
		if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
			configRoot = filepath.Join(home, configRoot)
		}
	}

	var profile string
	if value, ok := os.LookupEnv("OMP_PROFILE"); ok {
		profile = strings.TrimSpace(value)
	} else {
		profile = strings.TrimSpace(os.Getenv("PI_PROFILE"))
	}
	if profile != "" && profile != "default" {
		configRoot = filepath.Join(configRoot, "profiles", profile)
	}

	return filepath.Join(configRoot, "agent")
}
