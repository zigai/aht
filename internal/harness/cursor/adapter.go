package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
)

const (
	cursorCommand           = "cursor-agent"
	cursorIntegrationSource = "cursor-hook"
)

type cursorHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID      string   `json:"session_id"      validate:"required,notblank"`
	TranscriptPath *string  `json:"transcript_path" validate:"omitempty"`
	WorkspaceRoots []string `json:"workspace_roots" validate:"required,min=1,dive,notblank"`
	HookEventName  string   `json:"hook_event_name" validate:"required,notblank"`
	CursorVersion  string   `json:"cursor_version"  validate:"required,notblank"`
}

func New() cursorHarness {
	return cursorHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true, CatalogCreates: false,
		ID:           registry.Harness("cursor"),
		Aliases:      []string{"cursor-agent", "cursor_agent", "cursor-cli", "cursor_cli"},
		ProcessNames: []string{"cursor", "cursor-agent", "cursor-cli"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: []string{"CURSOR_TRANSCRIPT_PATH"},
			ProjectRoot: []string{"CURSOR_PROJECT_DIR", "CLAUDE_PROJECT_DIR"},
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
		IntegrationSource:  cursorIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (cursorHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.CursorJSONHooksAction{Plan: harness.CursorJSONHookInstallPlan{
		Path:        filepath.Join(cursorHome(), "hooks.json"),
		Source:      cursorIntegrationSource,
		Label:       "cursor hooks",
		ConfigLabel: "cursor config",
		Hooks: []harness.CursorCommandHookInstallSpec{
			{
				Event:   "sessionStart",
				Command: cursorHookCommand(binary, registry.ActivityIdle, "sessionStart", "{}"),
			},
			{
				Event:   "beforeSubmitPrompt",
				Command: cursorHookCommand(binary, registry.ActivityRunning, "beforeSubmitPrompt", `{"continue":true}`),
			},
			{
				Event:   "stop",
				Command: cursorHookCommand(binary, registry.ActivityIdle, "stop", "{}"),
			},
			{
				Event:   "sessionEnd",
				Command: cursorHookCommand(binary, registry.PresenceGone, "sessionEnd", "{}"),
			},
		},
	}}}}
}

func (cursorHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{cursorCommand, harness.ResumeFlag, sessionID}
}

func (cursorHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (cursorHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "cursor_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "cursor_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "cursor_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(attributes, "cursor_version", harness.PayloadString(payload, "cursor_version"))
	harness.AddAttributeString(attributes, "cursor_composer_mode", harness.PayloadString(payload, "composer_mode"))
	harness.AddAttributeString(attributes, "cursor_session_end_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "cursor_final_status", harness.PayloadString(payload, "final_status"))
	harness.AddAttributeString(attributes, "cursor_stop_status", harness.PayloadString(payload, "status"))
	harness.AddAttributeString(attributes, "cursor_is_background_agent", harness.PayloadBoolString(payload, "is_background_agent"))
	harness.AddAttributeString(attributes, "cursor_sandbox", harness.PayloadBoolString(payload, "sandbox"))

	projectRoot := harness.FirstArrayString(payload, "workspace_roots")
	cwd := harness.PayloadString(payload, "cwd")
	if cwd == "" {
		cwd = projectRoot
	}

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadStringAny(payload, "session_id", "conversation_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         cwd,
		ProjectRoot: projectRoot,
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func (cursorHarness) ObservableProcess(process processinfo.Process) bool {
	if process.TTY == "" && process.MultiplexerPane == "" && !slices.Contains(process.Args, "agent") {
		return false
	}
	return true
}

func cursorHookCommand[T harness.Transition](binary string, transition T, event string, hookOutput string) string {
	report := harness.RawStdinDefaultsReportHookCommand(binary, registry.Harness("cursor"), transition, event, cursorIntegrationSource)

	return report + " >/dev/null 2>&1 || true; printf '%s\\n' " + harness.ShellQuote(hookOutput)
}

func cursorHome() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".cursor")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cursor")
	}

	return ".cursor"
}

func (cursorHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["cursor_hook_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "cursor_start_source", "source", "reason"))
}
