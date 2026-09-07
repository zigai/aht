package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	claudeCommand           = "claude"
	claudeIntegrationSource = "claude-hook"
)

type claudeHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID      string  `json:"session_id"      validate:"required,notblank"`
	TranscriptPath *string `json:"transcript_path" validate:"omitempty"`
	CWD            string  `json:"cwd"             validate:"required,notblank"`
	HookEventName  string  `json:"hook_event_name" validate:"required,notblank"`
}

func New() claudeHarness {
	return claudeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessClaude,
		Aliases:      []string{"claude-code", "claude_code"},
		ProcessNames: []string{"claude", "claude-code"},
		Env: harness.EnvKeys{
			SessionID:   []string{"CLAUDE_SESSION_ID"},
			SessionPath: []string{"CLAUDE_SESSION_PATH"},
			ProjectRoot: nil,
			PID:         []string{"CLAUDE_PID"},
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
			ProcessIdentity:   false,
			NativeCatalog:     true,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
	})}
}

func (claudeHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
		Path:              filepath.Join(claudeConfigDir(), "settings.json"),
		Source:            claudeIntegrationSource,
		Label:             "claude hooks",
		ConfigLabel:       "claude config",
		StatusMessage:     "",
		OmitStatusMessage: false,
		HooksAtRoot:       false,
		Hooks: []harness.CommandHookInstallSpec{
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "startup|resume|clear|compact",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityIdle, harness.HookEventSessionStart, claudeIntegrationSource),
			},
			{
				Event:   harness.HookEventUserPromptSubmit,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, harness.HookEventUserPromptSubmit, claudeIntegrationSource),
			},
			{
				Event:   harness.HookEventPreToolUse,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, harness.HookEventPreToolUse, claudeIntegrationSource),
			},
			{
				Event:   harness.HookEventPostToolUse,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, harness.HookEventPostToolUse, claudeIntegrationSource),
			},
			{
				Event:   harness.HookEventPostToolUseFailure,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, harness.HookEventPostToolUseFailure, claudeIntegrationSource),
			},
			{
				Event:   "PermissionRequest",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityWaiting, "PermissionRequest", claudeIntegrationSource),
			},
			{
				Event:   "PermissionDenied",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityIdle, "PermissionDenied", claudeIntegrationSource),
			},
			{
				Event:   "Notification",
				Matcher: "permission_prompt",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityWaiting, "Notification", claudeIntegrationSource),
			},
			{
				Event:   "SubagentStart",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, "SubagentStart", claudeIntegrationSource),
			},
			{
				Event:   "SubagentStop",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityIdle, "SubagentStop", claudeIntegrationSource),
			},
			{
				Event:   "PreCompact",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityRunning, "PreCompact", claudeIntegrationSource),
			},
			{
				Event:   "PostCompact",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityIdle, "PostCompact", claudeIntegrationSource),
			},
			{
				Event:   harness.HookEventStop,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityIdle, harness.HookEventStop, claudeIntegrationSource),
			},
			{
				Event:   "StopFailure",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.ActivityFailed, "StopFailure", claudeIntegrationSource),
			},
			{
				Event:   "SessionEnd",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessClaude, registry.PresenceGone, "SessionEnd", claudeIntegrationSource),
			},
		},
	}}}}
}

func (claudeHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{claudeCommand, harness.ResumeFlag, sessionID}
}

func (claudeHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (claudeHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	return claudePayloadDefaults(payload), nil
}

func claudePayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "claude_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "claude_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "claude_permission_mode", harness.PayloadString(payload, "permission_mode"))
	harness.AddAttributeString(attributes, "claude_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(attributes, "claude_notification_type", harness.PayloadStringAny(payload, "notification_type", "type"))
	harness.AddAttributeString(attributes, "claude_session_end_reason", harness.PayloadString(payload, "reason"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}
}

func claudeConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".claude")
	}

	return ".claude"
}
