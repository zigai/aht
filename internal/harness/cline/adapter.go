package cline

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	clineCommand           = "cline"
	clinePluginName        = "aht-state"
	clineMarkerFileName    = ".aht-managed"
	clineIntegrationSource = "cline-plugin"
	integrationVersion     = 10
)

//go:embed assets/index.js.tmpl
var clinePluginTemplate string

type clineHarness struct{ harness.BaseAdapter }

func New() clineHarness {
	return clineHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessCline,
		Aliases:      nil,
		ProcessNames: []string{"cline"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: nil,
			PID:         nil,
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      false,
			SessionEnd:        false,
			RunningIdle:       true,
			WaitingPermission: false,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: integrationVersion,
	})}
}

func (clineHarness) InstallPlan(binary string) harness.InstallPlan {
	version := strconv.Itoa(integrationVersion)
	dir := filepath.Join(clineConfigDir(), "plugins", clinePluginName)

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   dir,
		Label: "Cline plugin",
		Files: []harness.RenderedFileInstallSpec{
			{Name: "package.json", Content: "", JSONContent: map[string]any{
				"name": clinePluginName, "version": "0.0." + version, "private": true, "type": "module",
				"cline": map[string]any{"plugins": []any{map[string]any{
					"paths": []string{"./index.js"}, "capabilities": []string{"hooks"},
				}}},
			}},
			{Name: "index.js", Content: renderClinePlugin(binary, version), JSONContent: nil},
			{Name: clineMarkerFileName, Content: clineMarkerContent(version), JSONContent: nil},
		},
		SnippetOrder:   []string{"package.json", "index.js", clineMarkerFileName},
		MarkerFile:     clineMarkerFileName,
		ObsoleteFiles:  clineLegacyHookPaths(),
		ImportManifest: nil,
		Registration:   nil,
	}}}}
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
	return clinePayloadDefaults(payload), nil
}

func renderClinePlugin(binary string, version string) string {
	replacer := strings.NewReplacer(
		"{{BINARY}}", strconv.Quote(binary),
		"{{INTEGRATION_VERSION}}", strconv.Quote(version),
	)

	return replacer.Replace(clinePluginTemplate)
}

func clineMarkerContent(version string) string {
	return fmt.Sprintf("%s\nAHT_INTEGRATION_ID=cline\nAHT_INTEGRATION_VERSION=%s\nAHT_SOURCE=%s\n", harness.ManagedMarker, version, clineIntegrationSource)
}

func clinePayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	sessionID := harness.FirstNonEmpty(harness.NestedString(payload, "sessionContext", "rootSessionId"), harness.PayloadString(payload, "taskId"))
	projectRoot := harness.FirstNonEmpty(harness.NestedString(payload, "workspaceInfo", "rootPath"), harness.FirstArrayString(payload, "workspaceRoots"))
	cwd := harness.FirstNonEmpty(harness.PayloadString(payload, "cwd"), projectRoot)

	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "cline_hook_event", harness.PayloadString(payload, "hookName"))
	harness.AddAttributeString(attributes, "cline_task_id", harness.PayloadString(payload, "taskId"))
	harness.AddAttributeString(attributes, "cline_version", harness.PayloadString(payload, "clineVersion"))
	harness.AddAttributeString(attributes, "cline_agent_id", harness.PayloadString(payload, "agent_id"))
	harness.AddAttributeString(attributes, "cline_parent_agent_id", harness.PayloadString(payload, "parent_agent_id"))
	harness.AddAttributeString(attributes, "cline_tool_name", harness.FirstNonEmpty(harness.NestedString(payload, "tool_call", "name"),
		harness.NestedString(payload, "tool_result", "name")))
	harness.AddAttributeString(attributes, "cline_reason", harness.PayloadString(payload, "reason"))

	return harness.PayloadDefaults{
		SessionID:   sessionID,
		SessionPath: clineSessionPath(sessionID),
		CWD:         cwd,
		ProjectRoot: projectRoot,
		Event:       harness.PayloadString(payload, "hookName"),
		Attributes:  attributes,
	}
}

func clinePayloadValidator(rawPayload json.RawMessage) bool {
	payload, ok := harness.PayloadObject(rawPayload)
	if !ok {
		return false
	}

	return harness.FirstNonEmpty(harness.NestedString(payload, "sessionContext", "rootSessionId"), harness.PayloadString(payload, "taskId")) != "" &&
		harness.PayloadString(payload, "hookName") != ""
}

func clineSessionPath(sessionID string) string {
	if sessionID == "" {
		return ""
	}

	return filepath.Join(clineSessionDir(), sessionID, sessionID+".messages.json")
}

func clineConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_DIR")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".cline")
	}

	return ".cline"
}

func clineLegacyHooksDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_HOOKS_DIR")); value != "" {
		return value
	}

	return filepath.Join(clineConfigDir(), "hooks")
}

func clineLegacyHookPaths() []string {
	names := []string{
		"TaskStart.sh",
		"TaskResume.sh",
		"UserPromptSubmit.sh",
		"PreToolUse.sh",
		"PostToolUse.sh",
		"TaskComplete.sh",
		"TaskCancel.sh",
		"TaskError.sh",
		"PreCompact.sh",
		"SessionShutdown.sh",
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(clineLegacyHooksDir(), name))
	}

	return paths
}

func clineSessionDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_SESSION_DATA_DIR")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("CLINE_DATA_DIR")); value != "" {
		return filepath.Join(value, "sessions")
	}

	return filepath.Join(clineConfigDir(), "data", "sessions")
}
