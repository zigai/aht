package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	grokHookFileName      = "aht-state.json"
	grokIntegrationSource = "grok-hook"
)

type grokHarness struct{ harness.BaseAdapter }

type grokHookSpec struct {
	event   string
	command string
}

func New() grokHarness {
	return grokHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessGrok,
		Aliases:      []string{"grok-build", "grok_build"},
		ProcessNames: []string{"grok", "grok-build"},
		Env: harness.EnvKeys{
			SessionID:   []string{"GROK_SESSION_ID"},
			SessionPath: nil,
			ProjectRoot: []string{"GROK_WORKSPACE_ROOT"},
			PID:         nil,
			Event:       []string{"GROK_HOOK_EVENT"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: false,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
		IntegrationSource:  grokIntegrationSource,
		StateAuthority:     harness.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (grokHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(grokHome(), "hooks", grokHookFileName),
		Label:       "grok hooks",
		ConfigLabel: "grok hooks",
		Content:     "",
		JSONContent: grokHookConfig(binary),
	}}}}
}

func (grokHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"grok", harness.ResumeFlag, sessionID}
}

func (grokHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return grokPayloadValidator(rawPayload)
}

func grokPayloadValidator(rawPayload json.RawMessage) bool {
	payload, ok := harness.PayloadObject(rawPayload)
	if !ok {
		return false
	}
	return harness.PayloadStringAny(payload, "sessionId", "session_id") != "" &&
		harness.PayloadStringAny(payload, "hookEventName", "hook_event_name") != "" &&
		harness.PayloadString(payload, "cwd") != "" &&
		harness.PayloadStringAny(payload, "workspaceRoot", "workspace_root") != ""
}

func (grokHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "grok_hook_event", harness.PayloadStringAny(payload, "hookEventName", "hook_event_name"))
	harness.AddAttributeString(attributes, "grok_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "grok_tool_name", harness.PayloadStringAny(payload, "toolName", "tool_name"))
	harness.AddAttributeString(attributes, "grok_notification_type", harness.PayloadStringAny(
		payload,
		"notificationType",
		"notification_type",
		"type",
	))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "sessionId", "session_id"),
		SessionPath: "",
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: harness.PayloadStringAny(payload, "workspaceRoot", "workspace_root"),
		Event:       harness.PayloadStringAny(payload, "hookEventName", "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func grokHookConfig(binary string) map[string]any {
	specs := []grokHookSpec{
		{
			event:   harness.HookEventSessionStart,
			command: grokHookCommand(binary, registry.ActivityIdle, harness.HookEventSessionStart),
		},
		{
			event:   harness.HookEventUserPromptSubmit,
			command: grokHookCommand(binary, registry.ActivityRunning, harness.HookEventUserPromptSubmit),
		},
		{
			event:   harness.HookEventPreToolUse,
			command: grokHookCommand(binary, registry.ActivityRunning, harness.HookEventPreToolUse),
		},
		{
			event:   harness.HookEventPostToolUse,
			command: grokHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUse),
		},
		{
			event:   harness.HookEventPostToolUseFailure,
			command: grokHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUseFailure),
		},
		{
			event:   "PermissionDenied",
			command: grokHookCommand(binary, registry.ActivityIdle, "PermissionDenied"),
		},
		{
			event:   "SubagentStart",
			command: grokHookCommand(binary, registry.ActivityRunning, "SubagentStart"),
		},
		{
			event:   "SubagentStop",
			command: grokHookCommand(binary, registry.ActivityIdle, "SubagentStop"),
		},
		{
			event:   "PreCompact",
			command: grokHookCommand(binary, registry.ActivityRunning, "PreCompact"),
		},
		{
			event:   "PostCompact",
			command: grokHookCommand(binary, registry.ActivityIdle, "PostCompact"),
		},
		{
			event:   harness.HookEventStop,
			command: grokHookCommand(binary, registry.ActivityIdle, harness.HookEventStop),
		},
		{
			event:   "StopFailure",
			command: grokHookCommand(binary, registry.ActivityFailed, "StopFailure"),
		},
		{
			event:   "SessionEnd",
			command: grokHookCommand(binary, registry.PresenceGone, "SessionEnd"),
		},
	}

	hooks := make(map[string]any)
	for _, spec := range specs {
		existing, ok := hooks[spec.event].([]any)
		if !ok {
			existing = nil
		}
		hooks[spec.event] = append(existing, grokCommandHookGroup(spec.command))
	}

	return map[string]any{"hooks": hooks}
}

func grokCommandHookGroup(command string) map[string]any {
	return map[string]any{
		"hooks": []any{
			map[string]any{
				"type":          harness.HookTypeCommand,
				"command":       command,
				"timeout":       float64(harness.HookTimeoutSeconds),
				"statusMessage": harness.ManagedMarker,
			},
		},
	}
}

func grokHookCommand[T harness.Transition](binary string, transition T, event string) string {
	return harness.ReportHookCommand(binary, registry.HarnessGrok, transition, event, grokIntegrationSource)
}

func grokHome() string {
	if value := strings.TrimSpace(os.Getenv("GROK_HOME")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".grok")
	}

	return ".grok"
}
