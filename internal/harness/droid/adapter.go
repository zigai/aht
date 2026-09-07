package droid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	droidCommand           = "droid"
	droidIntegrationSource = "droid-hook"
)

type droidHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID     string `json:"session_id"      validate:"required,notblank"`
	CWD           string `json:"cwd"             validate:"required,notblank"`
	HookEventName string `json:"hook_event_name" validate:"required,notblank"`
}

func New() droidHarness {
	return droidHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID: registry.HarnessDroid,
		Aliases: []string{
			"factory",
			"factory-droid",
			"factory_droid",
			"factory-cli",
			"factory_cli",
		},
		ProcessNames: []string{"droid"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: []string{"FACTORY_PROJECT_DIR"},
			PID:         nil,
			Event:       nil,
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
	})}
}

func (droidHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
		Path:              filepath.Join(droidConfigDir(), "hooks.json"),
		Source:            droidIntegrationSource,
		Label:             "droid hooks",
		ConfigLabel:       "factory config",
		StatusMessage:     "",
		OmitStatusMessage: true,
		HooksAtRoot:       true,
		Hooks: []harness.CommandHookInstallSpec{
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityIdle, harness.HookEventSessionStart),
			},
			{
				Event:   harness.HookEventUserPromptSubmit,
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityRunning, harness.HookEventUserPromptSubmit),
			},
			{
				Event:   harness.HookEventPreToolUse,
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityRunning, harness.HookEventPreToolUse),
			},
			{
				Event:   harness.HookEventPostToolUse,
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUse),
			},
			{
				Event:   "Notification",
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityWaiting, "Notification"),
			},
			{
				Event:   harness.HookEventStop,
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityIdle, harness.HookEventStop),
			},
			{
				Event:   "SubagentStop",
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityIdle, "SubagentStop"),
			},
			{
				Event:   "PreCompact",
				Matcher: "",
				Command: droidHookCommand(binary, registry.ActivityRunning, "PreCompact"),
			},
			{
				Event:   "SessionEnd",
				Matcher: "",
				Command: droidHookCommand(binary, registry.PresenceGone, "SessionEnd"),
			},
		},
	}}}}
}

func (droidHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{droidCommand, harness.ResumeFlag, sessionID}
}

func (droidHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (droidHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	return droidPayloadDefaults(payload), nil
}

func droidHookCommand[T harness.Transition](binary string, transition T, event string) string {
	return harness.RawStdinDefaultsReportHookCommand(binary, registry.HarnessDroid, transition, event, droidIntegrationSource)
}

func droidPayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "droid_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "droid_tool_name", harness.PayloadString(payload, "tool_name"))
	harness.AddAttributeString(attributes, "droid_permission_mode", harness.PayloadString(payload, "permission_mode"))
	harness.AddAttributeString(attributes, "droid_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "droid_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "droid_stop_hook_active", harness.PayloadBoolString(payload, "stop_hook_active"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}
}

func droidConfigDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".factory")
	}

	return ".factory"
}
