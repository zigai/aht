package agy

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/processinfo"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	agyCommand                     = "agy"
	agyPluginName                  = "aht-state"
	agyImportManifestName          = "import_manifest.json"
	agyImportSource                = "antigravity"
	agyImportComponent             = "hooks"
	agyHookSource                  = "agy-hook"
	agyHookAdditionalAttributeKeys = 3
	integrationVersion             = 12
)

type agyHarness struct{ harness.BaseAdapter }

func New() agyHarness {
	return agyHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionGitHub,
			Repo:      "google-antigravity/antigravity-cli",
			Asset:     "agy_cli_linux_x64.tar.gz",
			Directory: "agy",
			Install:   distributionInstall,
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("agy"),
		Aliases: []string{
			"antigravity",
			"antigravity-cli",
			"antigravity_cli",
			"google-antigravity",
			"google_antigravity",
		},
		ProcessNames: []string{"agy", "antigravity-cli"},
		Capabilities: harness.Capabilities{
			RunningIdle:       true,
			WaitingPermission: true,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  agyHookSource,
		StateAuthority:     registry.AuthorityHook,
	})}
}

func (adapter agyHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	configDir := agyConfigDir()
	if configDir == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir:   filepath.Join(configDir, "plugins", agyPluginName),
				Label: "agy plugin",
				Files: []harness.RenderedFileInstallSpec{
					{Name: "plugin.json", JSONContent: map[string]any{"name": agyPluginName}},
					{Name: "hooks.json", JSONContent: agyHookConfig(binary, definition.ID)},
				},
				Identity:       definition.Identity(),
				ImportManifest: agyImportManifest(configDir),
				Retired:        []harness.PluginDirectoryInstallPlan{agyRetiredPlugin()},
			}},
			harness.ShimAction{},
		},
	}, nil
}

func agyImportManifest(dir string) *harness.ImportManifestInstallPlan {
	return &harness.ImportManifestInstallPlan{
		Path:       filepath.Join(dir, agyImportManifestName),
		Name:       agyPluginName,
		Source:     agyImportSource,
		Components: []string{agyImportComponent},
	}
}

// agyRetiredPlugin describes where earlier integration versions installed the
// plugin, before agy moved plugin discovery to the shared config directory.
func agyRetiredPlugin() harness.PluginDirectoryInstallPlan {
	dir := agyApplicationDir()

	return harness.PluginDirectoryInstallPlan{
		Dir:            filepath.Join(dir, "plugins", agyPluginName),
		Label:          "agy plugin",
		ImportManifest: agyImportManifest(dir),
	}
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

func (agyHarness) HandleHook(
	_ context.Context,
	invocation harness.HookInvocation,
) (harness.HookResult, error) {
	invocation.Event = agyHookEvent(invocation.Payload, invocation.Event)

	return agyHandleHook(invocation), nil
}

func agyHookConfig(binary string, id registry.Harness) map[string]any {
	handler := func(event string) map[string]any {
		return map[string]any{
			"type":    harness.HookTypeCommand,
			"command": harness.NativeHookCommand(binary, id, event),
			"timeout": float64(harness.HookTimeoutSeconds),
		}
	}
	toolGroup := func(event string) map[string]any {
		return map[string]any{"matcher": "*", "hooks": []any{handler(event)}}
	}

	return map[string]any{
		agyPluginName: map[string]any{
			"PreInvocation":  []any{handler("PreInvocation")},
			"PostInvocation": []any{handler("PostInvocation")},
			"PreToolUse":     []any{toolGroup("PreToolUse")},
			"PostToolUse":    []any{toolGroup("PostToolUse")},
			"Stop":           []any{handler("Stop")},
		},
	}
}

func agyConfigDir() string {
	dir := agyGeminiDir()
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, "config")
}

func agyApplicationDir() string {
	dir := agyGeminiDir()
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, "antigravity-cli")
}

func agyGeminiDir() string {
	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".gemini")
}

func agyPayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	workspacePath := harness.FirstArrayString(payload, "workspacePaths", "workspace_paths")
	toolCWD := harness.NestedString(payload, "toolCall", "args", "Cwd")
	cwd := cmp.Or(toolCWD, workspacePath)

	attributes := make(map[string]string)
	harness.AddAttributeString(
		attributes,
		"agy_hook_event",
		harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"),
	)
	harness.AddAttributeString(
		attributes,
		"agy_tool_name",
		harness.NestedString(payload, "toolCall", "name"),
	)
	harness.AddAttributeString(
		attributes,
		"agy_termination_reason",
		harness.PayloadStringAny(payload, "terminationReason", "termination_reason"),
	)
	harness.AddAttributeString(attributes, "agy_error", harness.PayloadString(payload, "error"))
	harness.AddAttributeString(
		attributes,
		"agy_fully_idle",
		harness.PayloadBoolString(payload, "fullyIdle", "fully_idle"),
	)

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
	return cmp.Or(
		strings.TrimSpace(explicitEvent),
		harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"),
	)
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

	return registry.Observation{
		Harness: registry.Harness("agy"),
		At:      time.Time{},
		Subject: registry.ObservationIdentity{
			SessionID:   defaults.SessionID,
			SessionPath: defaults.SessionPath,
			CWD:         defaults.CWD,
			Attributes:  defaults.Attributes,
		},
		Evidence: &registry.Report{
			Lifecycle: nil,

			Reporter: registry.Reporter{
				Sequence:     nil,
				Integration:  agyHookSource,
				Version:      integrationVersion,
				MultiSession: false,
			},
			Event:    invocation.Event,
			Activity: activity,

			Attributes: agyHookAttributes(
				defaults.Attributes,
				invocation.Event,
			),
			DetailObservedAt: nil,

			Claim:    nil,
			Process:  nil,
			Location: nil,

			Listing: &registry.Listing{
				ResumeCommand: agyResumeCommand(defaults.SessionID),
				CWD:           defaults.CWD,
				ProjectRoot:   defaults.ProjectRoot,
				ProcessPID:    0,
				Current:       false,
			},

			Payload: invocation.RawPayload,
			Detail:  nil,
		},
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
	case "PreToolUse":
		if isAgyInputWaitingTool(payload) {
			activity = registry.ActivityWaiting
		} else {
			activity = registry.ActivityRunning
		}
	case "PostToolUse":
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
	case "PreToolUse":
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

	return attributes
}

func isAgyInputWaitingTool(payload map[string]any) bool {
	switch harness.NestedString(payload, "toolCall", "name") {
	case "ask_permission", "ask_question":
		return true
	default:
		return false
	}
}

func (agyHarness) ObservableProcess(process processinfo.Process) bool {
	// Electron child processes share the application executable but do not own agent sessions.
	for _, arg := range process.Args {
		if strings.HasPrefix(arg, "--type=") {
			return false
		}
	}

	return true
}
