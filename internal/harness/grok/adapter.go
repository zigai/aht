package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	grokHookFileName      = "aht-state.json"
	grokIntegrationSource = "grok-hook"
	integrationVersion    = 15
)

type grokHarness struct{ harness.BaseAdapter }

func New() grokHarness {
	return grokHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		ID:               registry.Harness("grok"),
		Aliases:          []string{"grok-build", "grok_build"},
		ProcessNames:     []string{"grok", "grok-build"},
		Env: harness.EnvKeys{
			Config:      []string{"GROK_HOME"},
			SessionID:   []string{"GROK_SESSION_ID"},
			ProjectRoot: []string{"GROK_WORKSPACE_ROOT"},
			Event:       []string{"GROK_HOOK_EVENT"},
		},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
		},
		LifecycleEventAttribute:   "grok_hook_event",
		LifecycleSourceAttributes: []string{"grok_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         grokIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (adapter grokHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := grokHome(harness.HomeDir())
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "hooks", grokHookFileName),
				Label:       "grok hooks",
				ConfigLabel: "grok hooks",
				JSONContent: grokHookConfig(binary, adapter.Definition()),
			}},
		},
	}, nil
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
	harness.AddAttributeString(
		attributes,
		"grok_hook_event",
		harness.PayloadStringAny(payload, "hookEventName", "hook_event_name"),
	)
	harness.AddAttributeString(
		attributes,
		"grok_start_source",
		harness.PayloadString(payload, "source"),
	)
	harness.AddAttributeString(
		attributes,
		"grok_tool_name",
		harness.PayloadStringAny(payload, "toolName", "tool_name"),
	)
	harness.AddAttributeString(attributes, "grok_notification_type", harness.PayloadStringAny(
		payload,
		"notificationType",
		"notification_type",
		"type",
	))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "sessionId", "session_id"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: harness.PayloadStringAny(payload, "workspaceRoot", "workspace_root"),
		Event:       harness.PayloadStringAny(payload, "hookEventName", "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func grokHookConfig(binary string, definition harness.Definition) map[string]any {
	specs := harness.CommandHookSpecs(binary, definition, []harness.CommandHook{
		{Event: "SessionStart", Transition: harness.HookActivityIdle},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
		{Event: "PermissionDenied", Transition: harness.HookActivityIdle},
		// SubagentStart and SubagentStop fire inside child agents while the
		// parent turn keeps running until Stop.
		{Event: "PreCompact", Transition: harness.HookActivityRunning},
		// Automatic compaction continues the running turn; only manual
		// compaction returns the session to idle.
		{Event: "PostCompact", Matcher: "manual", Transition: harness.HookActivityIdle},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		{Event: "StopFailure", Transition: harness.HookActivityFailed},
		{Event: "SessionEnd", Transition: harness.HookPresenceGone},
	})

	hooks := make(map[string]any)
	for _, spec := range specs {
		existing, ok := hooks[spec.Event].([]any)
		if !ok {
			existing = nil
		}

		hooks[spec.Event] = append(existing, grokCommandHookGroup(spec.Matcher, spec.Command))
	}

	return map[string]any{"hooks": hooks}
}

func grokCommandHookGroup(matcher string, command string) map[string]any {
	group := map[string]any{
		"hooks": []any{
			map[string]any{
				"type":          harness.HookTypeCommand,
				"command":       command,
				"timeout":       float64(harness.HookTimeoutSeconds),
				"statusMessage": harness.ManagedMarker,
			},
		},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}

	return group
}

func grokHome(home string) string {
	if value := strings.TrimSpace(os.Getenv("GROK_HOME")); value != "" {
		return value
	}

	if home == "" {
		return ""
	}

	return filepath.Join(home, ".grok")
}
