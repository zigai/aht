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
	integrationVersion    = 15
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
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@qwen-code/qwen-code",
			Directory: "qwen",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: true},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		PayloadOmissions: []string{"tool_input", "tool_response"},
		ExclusiveProcess: true,
		ID:               registry.Harness("qwen"),
		Aliases:          []string{"qwen-code", "qwen_code", "qwencode"},
		ProcessNames:     []string{"qwen"},
		Env: harness.EnvKeys{
			Config:      []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"},
			ProjectRoot: []string{"QWEN_PROJECT_DIR"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "qwen_hook_event",
		LifecycleSourceAttributes: []string{"qwen_source", "source", "qwen_reason", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         qwenIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (adapter qwenHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := configDirectory(harness.HomeDir())
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	hooks := []harness.CommandHook{
		{
			Event:      "SessionStart",
			Matcher:    "startup|resume|clear|branch",
			Transition: harness.HookActivityIdle,
		},
		// Automatic compaction fires SessionStart inside a turn that keeps
		// running, so compaction reports presence without activity.
		{
			Event:      "SessionStart",
			Matcher:    "compact",
			Transition: harness.HookPresenceLive,
		},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
		// PermissionRequest also fires for background agents, which then
		// deny the call without asking. The permission_prompt notification
		// fires only once the approval dialog is shown.
		{
			Event:      "Notification",
			Matcher:    "permission_prompt",
			Transition: harness.HookActivityWaiting,
		},
		// The interactive UI sends idle_prompt whenever it returns to the
		// input prompt, including after Esc cancels a turn or the user
		// rejects a tool call; neither path runs Stop.
		{Event: "Notification", Matcher: "idle_prompt", Transition: harness.HookActivityIdle},
		{Event: "PreCompact", Transition: harness.HookActivityRunning},
		// Automatic compaction continues the running turn; only manual
		// compaction returns the session to idle.
		{Event: "PostCompact", Matcher: "manual", Transition: harness.HookActivityIdle},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		{Event: "StopFailure", Transition: harness.HookActivityFailed},
		{Event: "SessionEnd", Transition: harness.HookPresenceGone},
	}
	for index := range hooks {
		hooks[index].Stdin = harness.ReportStdinDefaults
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
				HooksKey:    "hooks",
				Path:        filepath.Join(base, "settings.json"),
				Source:      qwenIntegrationSource,
				Label:       "qwen hooks",
				ConfigLabel: "qwen config",
				Hooks:       harness.CommandHookSpecs(binary, adapter.Definition(), hooks),
			}},
		},
	}, nil
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
	harness.AddAttributeString(
		attributes,
		"qwen_hook_event",
		harness.PayloadString(payload, "hook_event_name"),
	)
	harness.AddAttributeString(attributes, "qwen_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "qwen_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(
		attributes,
		"qwen_permission_mode",
		harness.PayloadString(payload, "permission_mode"),
	)
	harness.AddAttributeString(attributes, "qwen_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(
		attributes,
		"qwen_notification_type",
		harness.PayloadString(payload, "notification_type"),
	)
	harness.AddAttributeString(
		attributes,
		"qwen_tool_name",
		harness.PayloadString(payload, "tool_name"),
	)
	harness.AddAttributeString(
		attributes,
		"qwen_agent_id",
		harness.PayloadString(payload, "agent_id"),
	)

	if harness.PayloadString(payload, "hook_event_name") == "StopFailure" {
		harness.AddAttributeString(
			attributes,
			"qwen_error_type",
			harness.PayloadString(payload, "error"),
		)
	}

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}
