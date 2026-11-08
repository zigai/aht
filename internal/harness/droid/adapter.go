package droid

import (
	"encoding/json"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	droidCommand           = "droid"
	droidIntegrationSource = "droid-hook"
	integrationVersion     = 15
)

type droidHarness struct{ harness.BaseAdapter }

func New() droidHarness {
	return droidHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		PayloadOmissions: []string{"tool_input", "tool_response"},
		ExclusiveProcess: true,
		ID:               registry.Harness("droid"),
		Aliases: []string{
			"factory",
			"factory-droid",
			"factory_droid",
			"factory-cli",
			"factory_cli",
		},
		ProcessNames: []string{"droid"},
		Env: harness.EnvKeys{
			Config:      []string{"DROID_SESSIONS_DIR"},
			ProjectRoot: []string{"FACTORY_PROJECT_DIR"},
		},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
		},
		LifecycleEventAttribute:   "droid_hook_event",
		LifecycleSourceAttributes: []string{"droid_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         droidIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (adapter droidHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := droidConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	hooks := []harness.CommandHook{
		{Event: "SessionStart", Transition: harness.HookActivityIdle},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{
			Event:      "Notification",
			Matcher:    "permission_prompt",
			Transition: harness.HookActivityWaiting,
		},
		{Event: "Notification", Matcher: "idle_prompt", Transition: harness.HookActivityIdle},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		// SubagentStop fires when a Task sub-droid finishes while the
		// parent turn keeps running until Stop.
		{Event: "PreCompact", Transition: harness.HookActivityRunning},
		{Event: "SessionEnd", Transition: harness.HookPresenceGone},
	}
	for index := range hooks {
		hooks[index].Stdin = harness.ReportStdinDefaults
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
				HooksKey:    "",
				Path:        filepath.Join(base, "hooks.json"),
				Source:      droidIntegrationSource,
				Label:       "droid hooks",
				ConfigLabel: "factory config",
				Hooks:       harness.CommandHookSpecs(binary, adapter.Definition(), hooks),
			}},
		},
	}, nil
}

func (droidHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{droidCommand, harness.ResumeFlag, sessionID}
}

func (droidHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[harness.CommandHookPayload]()(rawPayload)
}

func (droidHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(
		attributes,
		"droid_notification_type",
		harness.PayloadString(payload, "notification_type"),
	)
	harness.AddAttributeString(
		attributes,
		"droid_hook_event",
		harness.PayloadString(payload, "hook_event_name"),
	)
	harness.AddAttributeString(
		attributes,
		"droid_tool_name",
		harness.PayloadString(payload, "tool_name"),
	)
	harness.AddAttributeString(
		attributes,
		"droid_permission_mode",
		harness.PayloadString(payload, "permission_mode"),
	)
	harness.AddAttributeString(attributes, "droid_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "droid_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(
		attributes,
		"droid_stop_hook_active",
		harness.PayloadBoolString(payload, "stop_hook_active"),
	)

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func droidConfigDir() string {
	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".factory")
}
