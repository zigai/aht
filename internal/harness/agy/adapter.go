package agy

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	agyCommand                     = "agy"
	agyPluginName                  = "aht-state"
	agyMarkerFileName              = ".aht-managed"
	agyImportManifestName          = "import_manifest.json"
	agyImportSource                = "antigravity"
	agyImportComponent             = "hooks"
	agyIntegrationID               = "agy"
	agyHookSource                  = "agy-hook"
	agyHookAdditionalAttributeKeys = 3
	integrationVersion             = 8
)

type agyHarness struct{ harness.BaseAdapter }

func New() agyHarness {
	return agyHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID: registry.HarnessAgy,
		Aliases: []string{
			"antigravity",
			"antigravity-cli",
			"antigravity_cli",
			"google-antigravity",
			"google_antigravity",
		},
		ProcessNames: []string{"agy", "antigravity-cli"},
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
			WaitingPermission: true,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: integrationVersion,
	})}
}

func (agyHarness) InstallPlan(binary string) harness.InstallPlan {
	configDir := agyConfigDir()

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   filepath.Join(configDir, "plugins", agyPluginName),
		Label: "agy plugin",
		Files: []harness.RenderedFileInstallSpec{
			{
				Name:        "plugin.json",
				Content:     "",
				JSONContent: map[string]any{"name": agyPluginName},
			},
			{
				Name:        "hooks.json",
				Content:     "",
				JSONContent: agyHookConfig(binary),
			},
			{
				Name:        agyMarkerFileName,
				Content:     agyMarkerContent(),
				JSONContent: nil,
			},
		},
		SnippetOrder:  []string{"plugin.json", "hooks.json", agyMarkerFileName},
		MarkerFile:    agyMarkerFileName,
		ObsoleteFiles: nil,
		ImportManifest: &harness.ImportManifestInstallPlan{
			Path:       filepath.Join(configDir, agyImportManifestName),
			Name:       agyPluginName,
			Source:     agyImportSource,
			Components: []string{agyImportComponent},
		},
		Registration: nil,
	}}, harness.ShimAction{}}}
}

func (agyHarness) ResumeCommand(sessionID string, _ string) []string {
	return agyResumeCommand(sessionID)
}

func (agyHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return agyPayloadValidator(rawPayload)
}

func agyPayloadValidator(rawPayload json.RawMessage) bool {
	payload, ok := harness.PayloadObject(rawPayload)
	if !ok {
		return false
	}
	return harness.PayloadStringAny(payload, "conversationId", "conversation_id") != "" &&
		harness.FirstArrayString(payload, "workspacePaths", "workspace_paths") != ""
}

func (agyHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	return agyPayloadDefaults(payload), nil
}

func (agyHarness) HandleHook(invocation harness.HookInvocation) harness.HookResult {
	invocation.Event = agyHookEvent(invocation.Payload, invocation.Event)

	return agyHandleHook(invocation)
}

func agyHookConfig(binary string) map[string]any {
	return map[string]any{
		agyPluginName: map[string]any{
			"PreInvocation":              []any{agyHookHandler(binary, "PreInvocation")},
			"PostInvocation":             []any{agyHookHandler(binary, "PostInvocation")},
			harness.HookEventPreToolUse:  []any{agyToolHookGroup(binary, harness.HookEventPreToolUse)},
			harness.HookEventPostToolUse: []any{agyToolHookGroup(binary, harness.HookEventPostToolUse)},
			harness.HookEventStop:        []any{agyHookHandler(binary, harness.HookEventStop)},
		},
	}
}

func agyToolHookGroup(binary string, event string) map[string]any {
	return map[string]any{
		"matcher": "*",
		"hooks": []any{
			agyHookHandler(binary, event),
		},
	}
}

func agyHookHandler(binary string, event string) map[string]any {
	return map[string]any{
		"type":    harness.HookTypeCommand,
		"command": agyHookCommand(binary, event),
		"timeout": float64(harness.HookTimeoutSeconds),
	}
}

func agyHookCommand(binary string, event string) string {
	return strings.Join([]string{
		harness.ShellQuote(binary),
		"--json",
		"hook",
		string(registry.HarnessAgy),
		"--event", harness.ShellQuote(event),
	}, " ")
}

func agyMarkerContent() string {
	return strings.Join([]string{
		harness.ManagedMarker,
		"AHT_INTEGRATION_ID=" + agyIntegrationID,
		"AHT_INTEGRATION_VERSION=" + strconv.Itoa(integrationVersion),
		"AHT_SOURCE=" + agyHookSource,
	}, "\n")
}

func agyConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("AGY_CONFIG_HOME")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".gemini", "antigravity-cli")
	}

	return filepath.Join(".gemini", "antigravity-cli")
}

func agyPayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	workspacePath := harness.FirstArrayString(payload, "workspacePaths", "workspace_paths")
	toolCWD := harness.NestedString(payload, "toolCall", "args", "Cwd")
	cwd := harness.FirstNonEmpty(toolCWD, workspacePath)

	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "agy_hook_event", harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"))
	harness.AddAttributeString(attributes, "agy_tool_name", harness.NestedString(payload, "toolCall", "name"))
	harness.AddAttributeString(attributes, "agy_termination_reason", harness.PayloadStringAny(payload, "terminationReason", "termination_reason"))
	harness.AddAttributeString(attributes, "agy_error", harness.PayloadString(payload, "error"))
	harness.AddAttributeString(attributes, "agy_fully_idle", harness.PayloadBoolString(payload, "fullyIdle", "fully_idle"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "conversationId", "conversation_id"),
		SessionPath: harness.PayloadStringAny(payload, "transcriptPath", "transcript_path"),
		CWD:         cwd,
		ProjectRoot: workspacePath,
		Event:       harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"),
		Attributes:  attributes,
	}
}

func agyHookEvent(payload map[string]any, explicitEvent string) string {
	return harness.FirstNonEmpty(explicitEvent, harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"))
}

func agyHandleHook(invocation harness.HookInvocation) harness.HookResult {
	report, ok := agyHookReport(invocation)

	return harness.HookResult{
		Report:   report,
		ReportOK: ok,
		Response: agyHookResponse(invocation.Event),
	}
}

func agyHookReport(invocation harness.HookInvocation) (registry.Observation, bool) {
	activity := agyActivityForHook(invocation.Event, invocation.Payload)
	if activity == nil {
		var observation registry.Observation
		return observation, false
	}

	defaults := agyPayloadDefaults(invocation.Payload)
	if defaults.SessionID == "" && defaults.SessionPath == "" {
		var observation registry.Observation
		return observation, false
	}

	return registry.Observation{ //nolint:exhaustruct_v5 // native activity and resume metadata only
		Source:   registry.ObservationSourceNative,
		Evidence: registry.ObservationEvidenceNativeEvent,
		Harness:  registry.HarnessAgy,
		Identity: registry.ObservationIdentity{
			SessionID:   defaults.SessionID,
			SessionPath: defaults.SessionPath,
		},
		Activity:    activity,
		NativeEvent: invocation.Event,
		Catalog: &registry.CatalogMetadata{
			ResumeCommand: agyResumeCommand(defaults.SessionID),
			CWD:           defaults.CWD,
			ProjectRoot:   defaults.ProjectRoot,
			ProcessPID:    0,
			Current:       false,
		},
		Attributes: agyHookAttributes(defaults.Attributes, invocation.Event),
		RawPayload: invocation.RawPayload,
	}, true
}

func agyResumeCommand(sessionID string) []string {
	if sessionID == "" {
		return nil
	}
	return []string{agyCommand, "--conversation", sessionID}
}

func agyActivityForHook(event string, payload map[string]any) *registry.Activity {
	var activity registry.Activity
	switch event {
	case "PreInvocation", "PostInvocation":
		activity = registry.ActivityRunning
	case harness.HookEventPreToolUse:
		if isAgyInputWaitingTool(payload) {
			activity = registry.ActivityWaiting
		} else {
			activity = registry.ActivityRunning
		}
	case harness.HookEventPostToolUse:
		if _, ok := payload["toolCall"].(map[string]any); !ok {
			return nil
		}
		activity = registry.ActivityRunning
	case "Stop":
		if harness.PayloadBool(payload, "fullyIdle", "fully_idle") {
			activity = registry.ActivityIdle
		} else {
			activity = registry.ActivityRunning
		}
	default:
		return nil
	}
	return &activity
}

func agyHookResponse(event string) map[string]any {
	switch event {
	case harness.HookEventPreToolUse:
		return map[string]any{"decision": "allow"}
	default:
		return map[string]any{}
	}
}

func agyHookAttributes(defaultAttributes map[string]string, event string) map[string]string {
	attributes := make(map[string]string, len(defaultAttributes)+agyHookAdditionalAttributeKeys)
	maps.Copy(attributes, defaultAttributes)
	if event != "" {
		attributes["agy_hook_event"] = event
	}
	attributes["aht_integration"] = agyHookSource
	attributes["aht_integration_version"] = strconv.Itoa(integrationVersion)
	return attributes
}

func isAgyInputWaitingTool(payload map[string]any) bool {
	switch agyToolName(payload) {
	case "ask_permission", "ask_question":
		return true
	default:
		return false
	}
}

func agyToolName(payload map[string]any) string {
	toolCall, ok := payload["toolCall"].(map[string]any)
	if !ok {
		return ""
	}

	name, ok := toolCall["name"].(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(name)
}
