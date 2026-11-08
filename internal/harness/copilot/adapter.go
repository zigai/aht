package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	copilotHookFileName      = "aht.json"
	copilotIntegrationSource = "copilot-hook"
	integrationVersion       = 15
)

type copilotHarness struct{ harness.BaseAdapter }

func New() copilotHarness {
	return copilotHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@github/copilot",
			Directory: "copilot",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		Env: harness.EnvKeys{Config: []string{"COPILOT_HOME"}},
		PayloadOmissions: []string{
			"toolInput",
			"toolArgs",
			"toolResult",
			"tool_input",
			"tool_result",
		},
		ExclusiveProcess: true,
		ID:               registry.Harness("copilot"),
		Aliases: []string{
			"github-copilot",
			"github_copilot",
			"copilot-cli",
			"copilot_cli",
			"github-copilot-cli",
			"github_copilot_cli",
		},
		ProcessNames: []string{"copilot"},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "copilot_hook_event",
		LifecycleSourceAttributes: []string{"copilot_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         copilotIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (copilotHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := copilotHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.RenderedFileAction{Plan: harness.RenderedFileInstallPlan{
				Path:        filepath.Join(base, "hooks", copilotHookFileName),
				Label:       "copilot hooks",
				ConfigLabel: "copilot hooks",
				JSONContent: copilotHookConfig(binary),
			}},
		},
	}, nil
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
	attributes := harness.PayloadAttributes(payload, map[string][]string{
		"copilot_notification_type": {"notificationType", "notification_type"},
		"copilot_hook_event":        {"hookEventName", "hook_event_name", "event"},
		"copilot_tool_name":         {"toolName", "tool_name"},
		"copilot_start_source":      {"source"},
		"copilot_reason":            {"reason"},
		"copilot_stop_reason":       {"stopReason", "stop_reason"},
		"copilot_error":             {"error"},
	})

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "sessionId", "session_id"),
		SessionPath: harness.PayloadStringAny(payload, "transcriptPath", "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadStringAny(payload, "hookEventName", "hook_event_name", "event"),
		Attributes:  attributes,
	}, nil
}

func copilotHookConfig(binary string) map[string]any {
	specs := []harness.CommandHook{
		// A new or resumed session can start after its initial prompt was submitted.
		{Event: "sessionStart", Transition: harness.HookPresenceLive},
		{Event: "userPromptSubmitted", Transition: harness.HookActivityRunning},
		{Event: "preToolUse", Transition: harness.HookActivityRunning},
		{Event: "permissionRequest", Transition: harness.HookActivityWaiting},
		{
			Event:      "notification",
			Transition: harness.HookActivityWaiting,
			Matcher:    "permission_prompt",
		},
		{Event: "postToolUse", Transition: harness.HookActivityRunning},
		{Event: "postToolUseFailure", Transition: harness.HookActivityRunning},
		{Event: "preCompact", Transition: harness.HookActivityRunning},
		{Event: "subagentStart", Transition: harness.HookActivityRunning},
		{Event: "subagentStop", Transition: harness.HookActivityIdle},
		{Event: "agentStop", Transition: harness.HookActivityIdle},
		{Event: "sessionEnd", Transition: harness.HookPresenceGone},
	}

	hooks := make(map[string][]any, len(specs))
	for i := range specs {
		specs[i].Stdin = harness.ReportStdinDefaults
	}

	for _, spec := range harness.CommandHookSpecs(binary, New().Definition(), specs) {
		hooks[spec.Event] = append(hooks[spec.Event], copilotCommandHook(spec))
	}

	return map[string]any{
		"version": float64(1),
		"hooks":   hooks,
	}
}

func copilotCommandHook(spec harness.CommandHookInstallSpec) map[string]any {
	hook := map[string]any{
		"type": harness.HookTypeCommand,
		"command": spec.Command + " --attribute " + harness.ShellQuote(
			"copilot_hook_event="+spec.Event,
		) + " >/dev/null 2>&1 || true",
		"timeoutSec": float64(harness.HookTimeoutSeconds),
		"env": map[string]any{
			"AHT_MARKER":              harness.ManagedMarker,
			"AHT_INTEGRATION_VERSION": strconv.Itoa(integrationVersion),
		},
	}
	if spec.Matcher != "" {
		hook["matcher"] = spec.Matcher
	}

	return hook
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

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".copilot")
}
