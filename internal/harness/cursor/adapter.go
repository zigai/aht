package cursor

import (
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	cursorCommand           = "cursor-agent"
	cursorIntegrationSource = "cursor-hook"
	integrationVersion      = 15
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
		ExclusiveProcess: true,
		ID:               registry.Harness("cursor"),
		Aliases:          []string{"cursor-agent", "cursor_agent", "cursor-cli", "cursor_cli"},
		ProcessNames:     []string{"cursor", "cursor-agent", "cursor-cli"},
		Env: harness.EnvKeys{
			Config:      []string{"CURSOR_CONFIG_DIR"},
			SessionPath: []string{"CURSOR_TRANSCRIPT_PATH"},
			ProjectRoot: []string{"CURSOR_PROJECT_DIR", "CLAUDE_PROJECT_DIR"},
		},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
		},
		LifecycleEventAttribute:   "cursor_hook_event",
		LifecycleSourceAttributes: []string{"cursor_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         cursorIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (cursorHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := cursorHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.JSONMergeAction{Plan: harness.JSONMergeInstallPlan{
				Path:        filepath.Join(base, "hooks.json"),
				Source:      cursorIntegrationSource,
				Label:       "cursor hooks",
				ConfigLabel: "cursor config",
				Merge: func(config map[string]any, isManaged func(string) bool) (bool, error) {
					return mergeHooks(config, hookSpecs(binary), isManaged)
				},
				Remove: func(config map[string]any, isManaged func(string) bool) bool {
					return removeHooks(config, hookSpecs(binary), isManaged)
				},
			}},
		},
	}, nil
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
	attributes := harness.PayloadAttributes(payload, map[string][]string{
		"cursor_hook_event":         {"hook_event_name"},
		"cursor_start_source":       {"source"},
		"cursor_model":              {"model"},
		"cursor_version":            {"cursor_version"},
		"cursor_composer_mode":      {"composer_mode"},
		"cursor_session_end_reason": {"reason"},
		"cursor_final_status":       {"final_status"},
		"cursor_stop_status":        {"status"},
	})

	harness.AddAttributeString(
		attributes,
		"cursor_is_background_agent",
		harness.PayloadBoolString(payload, "is_background_agent"),
	)
	harness.AddAttributeString(
		attributes,
		"cursor_sandbox",
		harness.PayloadBoolString(payload, "sandbox"),
	)

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
	if process.TTY == "" && drivers.LocationFromEnv(process.Env).PaneID == "" &&
		!slices.Contains(process.Args, "agent") {
		return false
	}

	return true
}

func cursorHome() string {
	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".cursor")
}

func hookSpecs(binary string) []harness.CommandHookInstallSpec {
	hooks := []harness.CommandHook{
		{
			Event:      "sessionStart",
			Transition: harness.HookActivityIdle,
			Stdin:      harness.ReportStdinDefaults,
		},
		{
			Event:      "beforeSubmitPrompt",
			Transition: harness.HookActivityRunning,
			Stdin:      harness.ReportStdinDefaults,
		},
		{Event: "stop", Transition: harness.HookActivityIdle, Stdin: harness.ReportStdinDefaults},
		{
			Event:      "sessionEnd",
			Transition: harness.HookPresenceGone,
			Stdin:      harness.ReportStdinDefaults,
		},
	}

	specs := harness.CommandHookSpecs(binary, New().Definition(), hooks)
	for i := range specs {
		output := "{}"
		if specs[i].Event == "beforeSubmitPrompt" {
			output = `{"continue":true}`
		}

		specs[i].Command += " >/dev/null 2>&1 || true; printf '%s\\n' " + harness.ShellQuote(output)
	}

	return specs
}
