package cline

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	clineCommand           = "cline"
	clinePluginName        = "aht-state"
	clineIntegrationSource = "cline-plugin"
	integrationVersion     = 12
)

//go:embed assets/index.js.tmpl
var clinePluginTemplate string

type clineHarness struct{ harness.BaseAdapter }

func New() clineHarness {
	return clineHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Env: harness.EnvKeys{
			Config: []string{
				"CLINE_DIR",
				"CLINE_DATA_DIR",
				"CLINE_SESSION_DATA_DIR",
				"CLINE_GLOBAL_SETTINGS_PATH",
				"CLINE_MCP_SETTINGS_PATH",
			},
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("cline"),
		ProcessNames:     []string{"cline"},
		Capabilities: harness.Capabilities{
			RunningIdle: true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  clineIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter clineHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := clineConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir:   filepath.Join(base, "plugins", clinePluginName),
				Label: "Cline plugin",
				Files: []harness.RenderedFileInstallSpec{
					{Name: "package.json", JSONContent: map[string]any{
						"name":    clinePluginName,
						"version": "0.0." + strconv.Itoa(integrationVersion),
						"private": true,
						"type":    "module",
						"cline": map[string]any{"plugins": []any{map[string]any{
							"paths": []string{"./index.js"}, "capabilities": []string{"hooks"},
						}}},
					}},
					{
						Name: "index.js",
						Content: harness.RenderScriptTemplate(
							clinePluginTemplate,
							binary,
							definition,
						),
					},
				},
				Identity: definition.Identity(),
			}},
		},
	}, nil
}

func (clineHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{clineCommand, "--id", sessionID}
}

func (clineHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return clinePayloadValidator(rawPayload)
}

func (clineHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	sessionID := cmp.Or(
		harness.NestedString(payload, "sessionContext", "rootSessionId"),
		harness.PayloadString(payload, "taskId"),
	)
	projectRoot := cmp.Or(
		harness.NestedString(payload, "workspaceInfo", "rootPath"),
		harness.FirstArrayString(payload, "workspaceRoots"),
	)
	cwd := cmp.Or(harness.PayloadString(payload, "cwd"), projectRoot)

	attributes := make(map[string]string)
	harness.AddAttributeString(
		attributes,
		"cline_hook_event",
		harness.PayloadString(payload, "hookName"),
	)
	harness.AddAttributeString(
		attributes,
		"cline_task_id",
		harness.PayloadString(payload, "taskId"),
	)
	harness.AddAttributeString(
		attributes,
		"cline_version",
		harness.PayloadString(payload, "clineVersion"),
	)
	harness.AddAttributeString(
		attributes,
		"cline_agent_id",
		harness.PayloadString(payload, "agent_id"),
	)
	harness.AddAttributeString(
		attributes,
		"cline_parent_agent_id",
		harness.PayloadString(payload, "parent_agent_id"),
	)
	harness.AddAttributeString(
		attributes,
		"cline_tool_name",
		cmp.Or(harness.NestedString(payload, "tool_call", "name"),
			harness.NestedString(payload, "tool_result", "name")),
	)
	harness.AddAttributeString(attributes, "cline_reason", harness.PayloadString(payload, "reason"))

	return harness.PayloadDefaults{
		SessionID:   sessionID,
		SessionPath: clineSessionPath(sessionID),
		CWD:         cwd,
		ProjectRoot: projectRoot,
		Event:       harness.PayloadString(payload, "hookName"),
		Attributes:  attributes,
	}, nil
}

func clinePayloadValidator(rawPayload json.RawMessage) bool {
	payload, ok := harness.PayloadObject(rawPayload)
	if !ok {
		return false
	}

	return cmp.Or(
		harness.NestedString(payload, "sessionContext", "rootSessionId"),
		harness.PayloadString(payload, "taskId"),
	) != "" &&
		harness.PayloadString(payload, "hookName") != ""
}

func clineSessionPath(sessionID string) string {
	if sessionID == "" {
		return ""
	}

	dir := clineSessionDir()
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, sessionID, sessionID+".messages.json")
}

func clineConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_DIR")); value != "" {
		return value
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".cline")
}

func clineSessionDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_SESSION_DATA_DIR")); value != "" {
		return value
	}

	data := clineDataDir()
	if data == "" {
		return ""
	}

	return filepath.Join(data, "sessions")
}
