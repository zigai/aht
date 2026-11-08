package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	codexCommand               = "codex"
	codexIntegrationSource     = "codex-hook"
	integrationVersion         = 16
	terminalHookTimeoutSeconds = 3
)

type codexHarness struct{ harness.BaseAdapter }

func New() codexHarness {
	return codexHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@openai/codex",
			Directory: "codex",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: true, Question: false, UsageLimit: true},
		},
		ScreenManifest:       screenManifest,
		PayloadOmissions:     []string{"tool_input", "tool_response"},
		ExclusiveProcess:     true,
		RetainNativeActivity: true,
		ID:                   registry.Harness("codex"),
		ProcessNames:         []string{"codex"},
		Env: harness.EnvKeys{
			Config:      []string{"CODEX_HOME", "CODEX_SQLITE_HOME"},
			SessionID:   []string{"CODEX_SESSION_ID"},
			SessionPath: []string{"CODEX_SESSION_PATH"},
			PID:         []string{"CODEX_PID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "codex_hook_event",
		LifecycleSourceAttributes: []string{"codex_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         codexIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
		ScreenFallback:            true,
	})}
}

func (adapter codexHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := codexHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	plan := harness.JSONCommandHookInstallPlan{
		HooksKey:      "hooks",
		Path:          filepath.Join(base, "hooks.json"),
		Source:        codexIntegrationSource,
		Label:         "codex hooks",
		ConfigLabel:   "codex config",
		StatusMessage: "Recording agent session",
		Hooks: harness.CommandHookSpecs(binary, adapter.Definition(), []harness.CommandHook{
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
			{Event: "PermissionRequest", Matcher: "*", Transition: harness.HookActivityWaiting},
			{
				Event:      "PostToolUse",
				Transition: harness.HookActivityRunning,
				Stdin:      harness.ReportStdinDefaults,
			},
			{Event: "PreCompact", Transition: harness.HookActivityRunning},
			// Automatic compaction continues the running turn; only manual
			// compaction returns the session to idle.
			{Event: "PostCompact", Matcher: "manual", Transition: harness.HookActivityIdle},
			// SubagentStart and SubagentStop describe child agents inside the
			// parent turn, which keeps running until Stop.
			{Event: "Stop", Transition: harness.HookActivityIdle},
			{
				Event:      "Interrupt",
				Transition: harness.HookActivityInterrupted,
				Timeout:    terminalHookTimeoutSeconds,
			},
			{
				Event:      "SessionEnd",
				Matcher:    "other",
				Transition: harness.HookPresenceGone,
				Timeout:    terminalHookTimeoutSeconds,
			},
		}),
	}
	plan.Trust = hookTrust{path: plan.Path, hooks: plan.Hooks}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.JSONCommandHooksAction{Plan: plan},
			harness.ShimAction{},
		},
	}, nil
}

func (codexHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{codexCommand, "resume", sessionID}
}

func (codexHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[harness.CommandHookPayload]()(rawPayload)
}

func (codexHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(
		attributes,
		"codex_hook_event",
		harness.PayloadString(payload, "hook_event_name"),
	)
	harness.AddAttributeString(
		attributes,
		"codex_start_source",
		harness.PayloadString(payload, "source"),
	)
	harness.AddAttributeString(
		attributes,
		"codex_permission_mode",
		harness.PayloadString(payload, "permission_mode"),
	)
	harness.AddAttributeString(attributes, "codex_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(
		attributes,
		"codex_turn_id",
		harness.PayloadString(payload, "turn_id"),
	)
	harness.AddAttributeString(
		attributes,
		"codex_session_end_reason",
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

func codexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".codex")
}
