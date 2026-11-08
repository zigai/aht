package openclaw

import (
	_ "embed"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	integrationVersion        = 11
	openclawCommand           = "openclaw"
	openclawPluginName        = "aht-state"
	openclawIntegrationSource = "openclaw-plugin"
	openclawSessionFlag       = "--session"
)

//go:embed assets/index.js.tmpl
var openclawPluginTemplate string

type openclawHarness struct{ harness.BaseAdapter }

func New() openclawHarness {
	return openclawHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Env: harness.EnvKeys{
			Config: []string{
				"OPENCLAW_HOME",
				"OPENCLAW_STATE_DIR",
				"OPENCLAW_CONFIG_PATH",
				"OPENCLAW_WORKSPACE_DIR",
				"OPENCLAW_PROFILE",
				"OPENCLAW_NIX_MODE",
			},
		},
		ID:           registry.Harness("openclaw"),
		ProcessNames: []string{"openclaw", "openclaw-gateway"},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  openclawIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter openclawHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	version := strconv.Itoa(integrationVersion)
	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir: filepath.Join(
					registry.DefaultStateDir(),
					"integrations",
					"openclaw",
					openclawPluginName,
				),
				Label: "OpenClaw plugin",
				Files: []harness.RenderedFileInstallSpec{
					{Name: "package.json", JSONContent: map[string]any{
						"name":     openclawPluginName,
						"version":  "0.0." + version,
						"private":  true,
						"type":     "module",
						"openclaw": map[string]any{"extensions": []string{"./index.js"}},
					}},
					{Name: "openclaw.plugin.json", JSONContent: map[string]any{
						"id":          openclawPluginName,
						"name":        "AHT State",
						"version":     "0.0." + version,
						"description": "Reports local OpenClaw session lifecycle and activity to aht.",
						"activation":  map[string]any{"onCapabilities": []string{"hook"}},
						"configSchema": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties":           map[string]any{},
						},
					}},
					{
						Name: "index.js",
						Content: harness.RenderScriptTemplate(
							openclawPluginTemplate,
							binary,
							definition,
						),
					},
				},
				Identity: definition.Identity(),
				Registration: newRegistration(
					openclawCommand,
					openclawPluginName,
					"0.0."+version,
					true,
				),
			}},
		},
	}, nil
}

func (openclawHarness) ResumeCommand(sessionID string, _ string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	return []string{openclawCommand, "tui", openclawSessionFlag, sessionID}
}
