package openclaw

import (
	_ "embed"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	integrationVersion        = 13
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
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "openclaw",
			Directory: "openclaw",
		},
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
	stateDir := integrationStateDir()
	if stateDir == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	version := strconv.Itoa(integrationVersion)
	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir: filepath.Join(
					stateDir,
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

func integrationStateDir() string {
	if dir := strings.TrimSpace(os.Getenv(registry.StateDirEnv)); dir != "" {
		return dir
	}

	if dir := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); dir != "" {
		return filepath.Join(dir, "aht")
	}

	if home := harness.HomeDir(); home != "" {
		return filepath.Join(home, ".local", "state", "aht")
	}

	return ""
}

func (openclawHarness) ResumeCommand(sessionID string, _ string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	return []string{openclawCommand, "tui", openclawSessionFlag, sessionID}
}
