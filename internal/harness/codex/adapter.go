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
	codexCommand           = "codex"
	codexIntegrationSource = "codex-hook"
)

type codexHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID      string  `json:"session_id"      validate:"required,notblank"`
	TranscriptPath *string `json:"transcript_path" validate:"omitempty"`
	CWD            string  `json:"cwd"             validate:"required,notblank"`
	HookEventName  string  `json:"hook_event_name" validate:"required,notblank"`
	Model          string  `json:"model"           validate:"omitempty"`
}

func New() codexHarness {
	return codexHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("codex"),
		Aliases:          nil,
		ProcessNames:     []string{"codex"},
		Env: harness.EnvKeys{
			SessionID:   []string{"CODEX_SESSION_ID"},
			SessionPath: []string{"CODEX_SESSION_PATH"},
			ProjectRoot: nil,
			PID:         []string{"CODEX_PID"},
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
		IntegrationSource:  codexIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     true,
	})}
}

func (codexHarness) RetainNativeActivity() bool { return true }

func (codexHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := codexHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
		Path:              filepath.Join(base, "hooks.json"),
		Source:            codexIntegrationSource,
		Label:             "codex hooks",
		ConfigLabel:       "codex config",
		StatusMessage:     "Recording agent session",
		OmitStatusMessage: false,
		HooksAtRoot:       false,
		Hooks: []harness.CommandHookInstallSpec{
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "startup|resume|clear",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityIdle, harness.HookEventSessionStart, codexIntegrationSource),
			},
			// Automatic compaction also fires SessionStart inside a turn that
			// keeps running, so compaction reports presence without activity.
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "compact",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.PresenceLive, harness.HookEventSessionStart, codexIntegrationSource),
			},
			{
				Event:   harness.HookEventUserPromptSubmit,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityRunning, harness.HookEventUserPromptSubmit, codexIntegrationSource),
			},
			{
				Event:   "PermissionRequest",
				Matcher: "*",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityWaiting, "PermissionRequest", codexIntegrationSource),
			},
			{
				Event:   harness.HookEventPostToolUse,
				Matcher: "",
				Command: harness.RawStdinDefaultsReportHookCommand(binary, registry.Harness("codex"), registry.ActivityRunning, harness.HookEventPostToolUse, codexIntegrationSource),
			},
			{
				Event:   "PreCompact",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityRunning, "PreCompact", codexIntegrationSource),
			},
			// Automatic compaction continues the running turn; only manual
			// compaction returns the session to idle.
			{
				Event:   "PostCompact",
				Matcher: "manual",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityIdle, "PostCompact", codexIntegrationSource),
			},
			// SubagentStart and SubagentStop describe child agents inside the
			// parent turn, which keeps running until Stop.
			{
				Event:   harness.HookEventStop,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityIdle, harness.HookEventStop, codexIntegrationSource),
			},
			{
				Event:   "Interrupt",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.ActivityInterrupted, "Interrupt", codexIntegrationSource),
			},
			{
				Event:   harness.HookEventSessionEnd,
				Matcher: "other",
				Command: harness.ReportHookCommand(binary, registry.Harness("codex"), registry.PresenceGone, harness.HookEventSessionEnd, codexIntegrationSource),
			},
		},
	}}, harness.ShimAction{}}}, nil
}

func (codexHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{codexCommand, "resume", sessionID}
}

func (codexHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (codexHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "codex_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "codex_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "codex_permission_mode", harness.PayloadString(payload, "permission_mode"))
	harness.AddAttributeString(attributes, "codex_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(attributes, "codex_turn_id", harness.PayloadString(payload, "turn_id"))
	harness.AddAttributeString(attributes, "codex_session_end_reason", harness.PayloadString(payload, "reason"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
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

func (codexHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["codex_hook_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "codex_start_source", "source", "reason"))
}

func (codexHarness) HookTimeoutSeconds(event string) int {
	const terminalTimeoutSeconds = 3
	if event == harness.HookEventSessionEnd || event == "Interrupt" {
		return terminalTimeoutSeconds
	}
	return harness.HookTimeoutSeconds
}
