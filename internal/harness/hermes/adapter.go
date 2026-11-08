package hermes

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	integrationVersion      = 10
	hermesCommand           = "hermes"
	hermesPluginName        = "aht-state"
	hermesIntegrationSource = "hermes-plugin"
)

//go:embed assets/__init__.py.tmpl
var hermesPluginTemplate string

type hermesHarness struct{ harness.BaseAdapter }

func New() hermesHarness {
	return hermesHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Env:              harness.EnvKeys{Config: []string{"HERMES_HOME", "HERMES_MANAGED"}},
		ExclusiveProcess: true,
		ID:               registry.Harness("hermes"),
		Aliases:          []string{"hermes-agent", "hermes_agent"},
		ProcessNames:     []string{"hermes", "hermes-agent"},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  hermesIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter hermesHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := hermesHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	version := strconv.Itoa(integrationVersion)
	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir:   filepath.Join(base, "plugins", hermesPluginName),
				Label: "Hermes plugin",
				Files: []harness.RenderedFileInstallSpec{
					{Name: "plugin.yaml", Content: hermesPluginManifest(version)},
					{
						Name: "__init__.py",
						Content: harness.RenderScriptTemplate(
							hermesPluginTemplate,
							binary,
							definition,
						),
					},
				},
				Identity:     definition.Identity(),
				Registration: newRegistration(hermesCommand, hermesPluginName, "0.0."+version),
			}},
		},
	}, nil
}

func (hermesHarness) ResumeCommand(sessionID string, _ string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	return []string{hermesCommand, harness.ResumeFlag, sessionID}
}

func hermesHome() string {
	if configured := strings.TrimSpace(os.Getenv("HERMES_HOME")); configured != "" {
		return configured
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".hermes")
}

func hermesPluginManifest(version string) string {
	return fmt.Sprintf(`name: aht-state
version: "0.0.%s"
description: Reports local Hermes session lifecycle and activity to aht.
provides_hooks:
  - on_session_start
  - pre_llm_call
  - on_session_end
  - on_session_finalize
  - on_session_reset
  - pre_approval_request
  - post_approval_response
`, version)
}
