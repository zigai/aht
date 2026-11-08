package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	claudeCommand           = "claude"
	claudeIntegrationSource = "claude-hook"
	integrationVersion      = 15
)

type claudeHarness struct{ harness.BaseAdapter }

func New() claudeHarness {
	return claudeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		PayloadOmissions: []string{"tool_input", "tool_response"},
		ExclusiveProcess: true, CatalogCreates: true,
		RetainNativeActivity: true,
		ID:                   registry.Harness("claude"),
		Aliases:              []string{"claude-code", "claude_code"},
		ProcessNames:         []string{"claude", "claude-code"},
		Env: harness.EnvKeys{
			Config:      []string{"CLAUDE_CONFIG_DIR"},
			SessionID:   []string{"CLAUDE_SESSION_ID"},
			SessionPath: []string{"CLAUDE_SESSION_PATH"},
			PID:         []string{"CLAUDE_PID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "claude_hook_event",
		LifecycleSourceAttributes: []string{"claude_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         claudeIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
		ScreenFallback:            true,
	})}
}

func (adapter claudeHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := claudeConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	hooks := harness.CommandHookSpecs(binary, adapter.Definition(), []harness.CommandHook{
		{
			Event:      "SessionStart",
			Matcher:    "startup|resume|clear",
			Transition: harness.HookActivityIdle,
		},
		// Automatic compaction also fires SessionStart inside a turn that
		// keeps running, so compaction reports presence without activity.
		{
			Event:      "SessionStart",
			Matcher:    "compact",
			Transition: harness.HookPresenceLive,
		},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
		{Event: "PermissionRequest", Transition: harness.HookActivityWaiting},
		{Event: "Elicitation", Transition: harness.HookActivityWaiting},
		{Event: "ElicitationResult", Transition: harness.HookActivityRunning},
		{Event: "PermissionDenied", Transition: harness.HookActivityIdle},
		{
			Event:      "Notification",
			Matcher:    "permission_prompt",
			Transition: harness.HookActivityWaiting,
		},
		// SubagentStart and SubagentStop describe child agents, including
		// Claude Code's internal prompt-suggestion agents, not the main
		// turn. Stop reports whether delegated work keeps the turn running.
		// Automatic compaction either happens inside a turn that already
		// runs or between turns, where no Stop would return the session to
		// idle, so only manual compaction reports running.
		{Event: "PreCompact", Matcher: "manual", Transition: harness.HookActivityRunning},
		// Automatic compaction continues the running turn; only manual
		// compaction returns the session to idle.
		{Event: "PostCompact", Matcher: "manual", Transition: harness.HookActivityIdle},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		{Event: "StopFailure", Transition: harness.HookActivityFailed},
		{Event: "SessionEnd", Transition: harness.HookPresenceGone},
	})

	for _, event := range []string{"SessionStart", "FileChanged"} {
		hooks = append(hooks, harness.CommandHookInstallSpec{
			Event:   event,
			Command: harness.NativeHookCommand(binary, adapter.Definition().ID, event),
		})
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
				StatusMessage: harness.ManagedMarker,
				HooksKey:      "hooks",
				Path:          filepath.Join(base, "settings.json"),
				Source:        claudeIntegrationSource,
				Label:         "claude hooks",
				ConfigLabel:   "claude config",
				Hooks:         hooks,
			}},
		},
	}, nil
}

func (claudeHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{claudeCommand, harness.ResumeFlag, sessionID}
}

func (claudeHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[harness.CommandHookPayload]()(rawPayload)
}

func (claudeHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)

	switch kind := harness.PayloadString(payload, "error"); kind {
	case "rate_limit", "billing_error":
		attributes["claude_error_type"] = kind
	}

	harness.AddAttributeString(
		attributes,
		"claude_hook_event",
		harness.PayloadString(payload, "hook_event_name"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_start_source",
		harness.PayloadString(payload, "source"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_permission_mode",
		harness.PayloadString(payload, "permission_mode"),
	)
	harness.AddAttributeString(attributes, "claude_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(
		attributes,
		"claude_notification_type",
		harness.PayloadStringAny(payload, "notification_type", "type"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_session_end_reason",
		harness.PayloadString(payload, "reason"),
	)

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func claudeConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		return value
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".claude")
}
