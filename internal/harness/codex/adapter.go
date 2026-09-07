package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
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
		ID:           registry.HarnessCodex,
		Aliases:      nil,
		ProcessNames: []string{"codex"},
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
	})}
}

func (codexHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.JSONCommandHooksAction{Plan: harness.JSONCommandHookInstallPlan{
		Path:              filepath.Join(codexHome(), "hooks.json"),
		Source:            codexIntegrationSource,
		Label:             "codex hooks",
		ConfigLabel:       "codex config",
		StatusMessage:     "Recording agent session",
		OmitStatusMessage: false,
		HooksAtRoot:       false,
		Hooks: []harness.CommandHookInstallSpec{
			{
				Event:   harness.HookEventSessionStart,
				Matcher: "startup|resume|clear|compact",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityIdle, harness.HookEventSessionStart, codexIntegrationSource),
			},
			{
				Event:   harness.HookEventUserPromptSubmit,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityRunning, harness.HookEventUserPromptSubmit, codexIntegrationSource),
			},
			{
				Event:   "PermissionRequest",
				Matcher: "*",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityWaiting, "PermissionRequest", codexIntegrationSource),
			},
			{
				Event:   harness.HookEventPostToolUse,
				Matcher: "",
				Command: harness.RawStdinDefaultsReportHookCommand(binary, registry.HarnessCodex, registry.ActivityRunning, harness.HookEventPostToolUse, codexIntegrationSource),
			},
			{
				Event:   "PreCompact",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityRunning, "PreCompact", codexIntegrationSource),
			},
			{
				Event:   "PostCompact",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityIdle, "PostCompact", codexIntegrationSource),
			},
			{
				Event:   "SubagentStart",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityRunning, "SubagentStart", codexIntegrationSource),
			},
			{
				Event:   "SubagentStop",
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityIdle, "SubagentStop", codexIntegrationSource),
			},
			{
				Event:   harness.HookEventStop,
				Matcher: "",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.ActivityIdle, harness.HookEventStop, codexIntegrationSource),
			},
			{
				Event:   harness.HookEventSessionEnd,
				Matcher: "other",
				Command: harness.ReportHookCommand(binary, registry.HarnessCodex, registry.PresenceGone, harness.HookEventSessionEnd, codexIntegrationSource),
			},
		},
	}}, harness.ShimAction{}}}
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
	return codexPayloadDefaults(payload), nil
}

func codexPayloadDefaults(payload map[string]any) harness.PayloadDefaults {
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
	}
}

func codexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".codex")
	}

	return ".codex"
}
