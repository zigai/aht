package goose

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	gooseCommand           = "goose"
	goosePluginName        = "aht-state"
	gooseIntegrationSource = "goose-hook"
	integrationVersion     = 15
)

type gooseHarness struct{ harness.BaseAdapter }

type goosePayload struct {
	SessionID string `json:"session_id" validate:"notblank"`
}

func New() gooseHarness {
	return gooseHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Env:              harness.EnvKeys{Config: []string{"GOOSE_PATH_ROOT"}},
		ExclusiveProcess: true,
		ID:               registry.Harness("goose"),
		ProcessNames:     []string{"goose"},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
		},
		LifecycleEventAttribute:   "goose_event",
		LifecycleSourceAttributes: []string{"goose_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         gooseIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (adapter gooseHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := goosePluginsDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	definition := adapter.Definition()

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir:   filepath.Join(base, goosePluginName),
				Label: "goose plugin",
				Files: []harness.RenderedFileInstallSpec{
					{
						Name: "plugin.json",
						JSONContent: map[string]any{
							"name":        goosePluginName,
							"version":     integrationVersion,
							"description": harness.ManagedMarker,
						},
					},
					{Name: "hooks/hooks.json", JSONContent: gooseHookConfig()},
					{Name: "scripts/report.sh", Content: gooseReportScript(binary, definition)},
				},
				Identity: definition.Identity(),
			}},
		},
	}, nil
}

func (gooseHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{gooseCommand, "session", harness.ResumeFlag, "--session-id", sessionID}
}

func (gooseHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[goosePayload]()(rawPayload)
}

func (gooseHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := harness.PayloadAttributes(payload, map[string][]string{
		"goose_event":           {"event"},
		"goose_start_source":    {"source"},
		"goose_tool_name":       {"tool_name"},
		"goose_matcher_context": {"matcher_context"},
	})

	cwd := harness.PayloadString(payload, "working_dir")

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		CWD:         cwd,
		ProjectRoot: cwd,
		Event:       harness.PayloadString(payload, "event"),
		Attributes:  attributes,
	}, nil
}

func gooseHookConfig() map[string]any {
	hooks := make(map[string]any)
	for _, spec := range gooseHookSpecs() {
		hooks[spec.Event] = []any{gooseHookRule(spec)}
	}

	return map[string]any{"hooks": hooks}
}

func gooseHookSpecs() []harness.CommandHook {
	return []harness.CommandHook{
		{Event: "SessionStart", Transition: harness.HookActivityIdle},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
		{Event: "BeforeReadFile", Transition: harness.HookActivityRunning},
		{Event: "AfterFileEdit", Transition: harness.HookActivityRunning},
		{Event: "BeforeShellExecution", Transition: harness.HookActivityRunning},
		{Event: "AfterShellExecution", Transition: harness.HookActivityRunning},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		{Event: "SessionEnd", Transition: harness.HookPresenceGone},
	}
}

func gooseHookRule(spec harness.CommandHook) map[string]any {
	return harness.CommandHookGroup(
		gooseHookCommand(spec),
		spec.Matcher,
		"",
		harness.HookTimeoutSeconds,
	)
}

func gooseHookCommand(spec harness.CommandHook) string {
	return strings.Join([]string{
		"sh",
		"\"${PLUGIN_ROOT}/scripts/report.sh\"",
		harness.ShellQuote(spec.Transition.State()),
		harness.ShellQuote(spec.Event),
	}, " ")
}

func gooseReportScript(binary string, definition harness.Definition) string {
	report := func(flag string) string {
		return "  " + harness.ReportScriptCommand(
			binary,
			definition,
			harness.ReportStdinDefaults,
			flag,
			`"$transition"`,
			`"$event"`,
		) + " >/dev/null 2>&1 || true"
	}

	return "#!/bin/sh\n" + definition.Identity().Marker("# ") + strings.Join([]string{
		"transition=${1:-}",
		"event=${2:-}",
		`if [ -z "$transition" ] || [ -z "$event" ]; then`,
		"  exit 0",
		"fi",
		"if [ \"$transition\" = gone ]; then",
		report("--presence"),
		"else",
		report("--activity"),
		"fi",
		"",
	}, "\n")
}

func goosePluginsDir() string {
	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".agents", "plugins")
}
