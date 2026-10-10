package qwen

import (
	"encoding/json"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	qwenCommand           = "qwen"
	qwenIntegrationSource = "qwen-hook"
)

type qwenHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID      string  `json:"session_id"      validate:"required,notblank"`
	TranscriptPath *string `json:"transcript_path" validate:"omitempty"`
	CWD            string  `json:"cwd"             validate:"required,notblank"`
	HookEventName  string  `json:"hook_event_name" validate:"required,notblank"`
	AgentID        *string `json:"agent_id"        validate:"isdefault"`
}

func New() qwenHarness {
	return qwenHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("qwen"),
		Aliases:          []string{"qwen-code", "qwen_code", "qwencode"},
		ProcessNames:     []string{"qwen"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: []string{"QWEN_PROJECT_DIR"},
			PID:         nil,
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
		IntegrationSource:  qwenIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (qwenHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := configDirectory(harness.HomeDir())
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
		Path:              filepath.Join(base, "settings.json"),
		Source:            qwenIntegrationSource,
		Label:             "qwen hooks",
		ConfigLabel:       "qwen config",
		StatusMessage:     "",
		OmitStatusMessage: true,
		HooksAtRoot:       false,
		Trust:             nil,
		Hooks: []harness.CommandHookInstallSpec{
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "startup|resume|clear|branch",
				Command: qwenHookCommand(binary, registry.ActivityIdle, harness.HookEventSessionStart),
			},
			// Automatic compaction fires SessionStart inside a turn that keeps
			// running, so compaction reports presence without activity.
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "compact",
				Command: qwenHookCommand(binary, registry.PresenceLive, harness.HookEventSessionStart),
			},
			{
				Event:   harness.HookEventUserPromptSubmit,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityRunning, harness.HookEventUserPromptSubmit),
			},
			{
				Event:   harness.HookEventPreToolUse,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityRunning, harness.HookEventPreToolUse),
			},
			{
				Event:   harness.HookEventPostToolUse,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUse),
			},
			{
				Event:   harness.HookEventPostToolUseFailure,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUseFailure),
			},
			// PermissionRequest also fires for background agents, which then
			// deny the call without asking. The permission_prompt notification
			// fires only once the approval dialog is shown.
			{
				Event:   "Notification",
				Matcher: "permission_prompt",
				Command: qwenHookCommand(binary, registry.ActivityWaiting, "Notification"),
			},
			// The interactive UI sends idle_prompt whenever it returns to the
			// input prompt, including after Esc cancels a turn or the user
			// rejects a tool call; neither path runs Stop.
			{
				Event:   "Notification",
				Matcher: "idle_prompt",
				Command: qwenHookCommand(binary, registry.ActivityIdle, "Notification"),
			},
			{
				Event:   "PreCompact",
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityRunning, "PreCompact"),
			},
			// Automatic compaction continues the running turn; only manual
			// compaction returns the session to idle.
			{
				Event:   "PostCompact",
				Matcher: "manual",
				Command: qwenHookCommand(binary, registry.ActivityIdle, "PostCompact"),
			},
			{
				Event:   harness.HookEventStop,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityIdle, harness.HookEventStop),
			},
			{
				Event:   "StopFailure",
				Matcher: "",
				Command: qwenHookCommand(binary, registry.ActivityFailed, "StopFailure"),
			},
			{
				Event:   harness.HookEventSessionEnd,
				Matcher: "",
				Command: qwenHookCommand(binary, registry.PresenceGone, harness.HookEventSessionEnd),
			},
		},
	}}}}, nil
}

func (qwenHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{qwenCommand, harness.ResumeFlag, sessionID}
}

func (qwenHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (qwenHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "qwen_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "qwen_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "qwen_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "qwen_permission_mode", harness.PayloadString(payload, "permission_mode"))
	harness.AddAttributeString(attributes, "qwen_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(attributes, "qwen_notification_type", harness.PayloadString(payload, "notification_type"))
	harness.AddAttributeString(attributes, "qwen_tool_name", harness.PayloadString(payload, "tool_name"))
	harness.AddAttributeString(attributes, "qwen_agent_id", harness.PayloadString(payload, "agent_id"))
	if harness.PayloadString(payload, "hook_event_name") == "StopFailure" {
		harness.AddAttributeString(attributes, "qwen_error_type", harness.PayloadString(payload, "error"))
	}

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func (qwenHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["qwen_hook_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "qwen_source", "source", "qwen_reason", "reason"))
}

func qwenHookCommand[T harness.Transition](binary string, transition T, event string) string {
	return harness.RawStdinDefaultsReportHookCommand(binary, registry.Harness("qwen"), transition, event, qwenIntegrationSource)
}
