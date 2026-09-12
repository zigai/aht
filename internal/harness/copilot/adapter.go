package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	copilotHookFileName      = "aht.json"
	copilotIntegrationSource = "copilot-hook"
)

type copilotHarness struct{ harness.BaseAdapter }

type copilotHookSpec struct {
	event      string
	transition harness.HookTransition
	matcher    string
}

func New() copilotHarness {
	return copilotHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessCopilot,
		Aliases:      []string{"github-copilot", "github_copilot", "copilot-cli", "copilot_cli", "github-copilot-cli", "github_copilot_cli"},
		ProcessNames: []string{"copilot"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: nil,
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
	})}
}

func (copilotHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
		Path:        filepath.Join(copilotHome(), "hooks", copilotHookFileName),
		Label:       "copilot hooks",
		ConfigLabel: "copilot hooks",
		Content:     "",
		JSONContent: copilotHookConfig(binary),
	}}}}
}

func (copilotHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{"copilot", "--resume", sessionID}
}

func (copilotHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return copilotPayloadValidator(rawPayload)
}

func (copilotHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	return copilotPayloadDefaults(payload), nil
}

func copilotHookConfig(binary string) map[string]any {
	specs := []copilotHookSpec{
		// A new or resumed session can start after its initial prompt was submitted.
		{event: "sessionStart", transition: harness.HookPresenceLive, matcher: ""},
		{event: "userPromptSubmitted", transition: harness.HookActivityRunning, matcher: ""},
		{event: "preToolUse", transition: harness.HookActivityRunning, matcher: ""},
		{event: "permissionRequest", transition: harness.HookActivityWaiting, matcher: ""},
		{event: "notification", transition: harness.HookActivityWaiting, matcher: "permission_prompt"},
		{event: "postToolUse", transition: harness.HookActivityRunning, matcher: ""},
		{event: "postToolUseFailure", transition: harness.HookActivityFailed, matcher: ""},
		{event: "preCompact", transition: harness.HookActivityRunning, matcher: ""},
		{event: "subagentStart", transition: harness.HookActivityRunning, matcher: ""},
		{event: "subagentStop", transition: harness.HookActivityIdle, matcher: ""},
		{event: "agentStop", transition: harness.HookActivityIdle, matcher: ""},
		{event: "sessionEnd", transition: harness.HookPresenceGone, matcher: ""},
	}

	hooks := make(map[string][]any, len(specs))
	for _, spec := range specs {
		hooks[spec.event] = append(hooks[spec.event], copilotCommandHook(binary, spec))
	}

	return map[string]any{
		"version": float64(1),
		"hooks":   hooks,
	}
}

func copilotCommandHook(binary string, spec copilotHookSpec) map[string]any {
	hook := map[string]any{
		"type":       harness.HookTypeCommand,
		"command":    copilotHookCommand(binary, spec.transition, spec.event),
		"timeoutSec": float64(harness.HookTimeoutSeconds),
		"env": map[string]any{
			"AHT_MARKER":              harness.ManagedMarker,
			"AHT_INTEGRATION_VERSION": strconv.Itoa(harness.IntegrationVersion),
		},
	}
	if spec.matcher != "" {
		hook["matcher"] = spec.matcher
	}

	return hook
}

func copilotHookCommand(binary string, transition harness.HookTransition, event string) string {
	return harness.RawStdinDefaultsReportHookCommand(binary, registry.HarnessCopilot, transition, event, copilotIntegrationSource) +
		" --attribute " + harness.ShellQuote("copilot_hook_event="+event) +
		" >/dev/null 2>&1 || true"
}

func copilotPayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "copilot_hook_event", harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"))
	harness.AddAttributeString(attributes, "copilot_tool_name", harness.PayloadStringAny(payload, "toolName", "tool_name"))
	harness.AddAttributeString(attributes, "copilot_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "copilot_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "copilot_stop_reason", harness.PayloadStringAny(payload, "stopReason", "stop_reason"))
	harness.AddAttributeString(attributes, "copilot_error", harness.PayloadString(payload, "error"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "sessionId", "session_id"),
		SessionPath: harness.PayloadStringAny(payload, "transcriptPath", "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"),
		Attributes:  attributes,
	}
}

func copilotPayloadValidator(rawPayload json.RawMessage) bool {
	payload, ok := harness.PayloadObject(rawPayload)
	if !ok {
		return false
	}

	return harness.PayloadStringAny(payload, "sessionId", "session_id") != "" &&
		harness.PayloadString(payload, "cwd") != ""
}

func copilotHome() string {
	if value := strings.TrimSpace(os.Getenv("COPILOT_HOME")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".copilot")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".copilot")
	}

	return ".copilot"
}
