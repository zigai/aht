package goose

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
	gooseCommand           = "goose"
	goosePluginName        = "aht-state"
	gooseMarkerFileName    = ".aht-managed"
	gooseIntegrationID     = "goose"
	gooseIntegrationSource = "goose-hook"
)

type gooseHarness struct{ harness.BaseAdapter }

type gooseHookSpec struct {
	event      string
	transition harness.HookTransition
	matcher    string
}

type goosePayload struct {
	SessionID string `json:"session_id" validate:"notblank"`
}

func New() gooseHarness {
	return gooseHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessGoose,
		Aliases:      nil,
		ProcessNames: []string{"goose"},
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
			NativeCatalog:     true,
			WaitingPermission: false,
			ProcessIdentity:   false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
		IntegrationSource:  gooseIntegrationSource,
		StateAuthority:     harness.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (gooseHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   filepath.Join(goosePluginsDir(), goosePluginName),
		Label: "goose plugin",
		Files: []harness.RenderedFileInstallSpec{
			{
				Name:    "plugin.json",
				Content: "",
				JSONContent: map[string]any{
					"name":        goosePluginName,
					"version":     harness.IntegrationVersion,
					"description": harness.ManagedMarker,
				},
			},
			{
				Name:        "hooks/hooks.json",
				Content:     "",
				JSONContent: gooseHookConfig(),
			},
			{
				Name:        "scripts/report.sh",
				Content:     gooseReportScript(binary),
				JSONContent: nil,
			},
			{
				Name:        gooseMarkerFileName,
				Content:     gooseMarkerContent(),
				JSONContent: nil,
			},
		},
		SnippetOrder:   []string{"plugin.json", "hooks/hooks.json", "scripts/report.sh", gooseMarkerFileName},
		MarkerFile:     gooseMarkerFileName,
		ObsoleteFiles:  nil,
		ImportManifest: nil,
		Registration:   nil,
	}}}}
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
	return goosePayloadDefaults(payload), nil
}

func gooseHookConfig() map[string]any {
	hooks := make(map[string]any)
	for _, spec := range gooseHookSpecs() {
		hooks[spec.event] = []any{gooseHookRule(spec)}
	}

	return map[string]any{"hooks": hooks}
}

func gooseHookSpecs() []gooseHookSpec {
	return []gooseHookSpec{
		{event: harness.HookEventSessionStart, transition: harness.HookActivityIdle, matcher: ""},
		{event: harness.HookEventUserPromptSubmit, transition: harness.HookActivityRunning, matcher: ""},
		{event: harness.HookEventPreToolUse, transition: harness.HookActivityRunning, matcher: ""},
		{event: harness.HookEventPostToolUse, transition: harness.HookActivityRunning, matcher: ""},
		{event: harness.HookEventPostToolUseFailure, transition: harness.HookActivityRunning, matcher: ""},
		{event: "BeforeReadFile", transition: harness.HookActivityRunning, matcher: ""},
		{event: "AfterFileEdit", transition: harness.HookActivityRunning, matcher: ""},
		{event: "BeforeShellExecution", transition: harness.HookActivityRunning, matcher: ""},
		{event: "AfterShellExecution", transition: harness.HookActivityRunning, matcher: ""},
		{event: harness.HookEventStop, transition: harness.HookActivityIdle, matcher: ""},
		{event: "SessionEnd", transition: harness.HookPresenceGone, matcher: ""},
	}
}

func gooseHookRule(spec gooseHookSpec) map[string]any {
	rule := map[string]any{
		"hooks": []any{
			gooseCommandHook(spec),
		},
	}
	if spec.matcher != "" {
		rule["matcher"] = spec.matcher
	}

	return rule
}

func gooseCommandHook(spec gooseHookSpec) map[string]any {
	return map[string]any{
		"type":    harness.HookTypeCommand,
		"command": gooseHookCommand(spec),
		"timeout": float64(harness.HookTimeoutSeconds),
	}
}

func gooseHookCommand(spec gooseHookSpec) string {
	return strings.Join([]string{
		"sh",
		"\"${PLUGIN_ROOT}/scripts/report.sh\"",
		harness.ShellQuote(spec.transition.State()),
		harness.ShellQuote(spec.event),
	}, " ")
}

func gooseReportScript(binary string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"# " + harness.ManagedMarker,
		"# AHT_INTEGRATION_ID=" + gooseIntegrationID,
		"# AHT_INTEGRATION_VERSION=" + strconv.Itoa(harness.IntegrationVersion),
		"# AHT_SOURCE=" + gooseIntegrationSource,
		"transition=${1:-}",
		"event=${2:-}",
		`if [ -z "$transition" ] || [ -z "$event" ]; then`,
		"  exit 0",
		"fi",
		"if [ \"$transition\" = gone ]; then",
		"  " + harness.ShellQuote(binary) + " report " + harness.ShellQuote(string(registry.HarnessGoose)) + " --presence \"$transition\" --event \"$event\" --attribute " + harness.ShellQuote("aht_integration_version="+strconv.Itoa(harness.IntegrationVersion)) + " --attribute " + harness.ShellQuote("aht_integration="+gooseIntegrationSource) + " --raw-stdin-defaults-only --quiet >/dev/null 2>&1 || true",
		"else",
		"  " + harness.ShellQuote(binary) + " report " + harness.ShellQuote(string(registry.HarnessGoose)) + " --activity \"$transition\" --event \"$event\" --attribute " + harness.ShellQuote("aht_integration_version="+strconv.Itoa(harness.IntegrationVersion)) + " --attribute " + harness.ShellQuote("aht_integration="+gooseIntegrationSource) + " --raw-stdin-defaults-only --quiet >/dev/null 2>&1 || true",
		"fi",
		"",
	}, "\n")
}

func gooseMarkerContent() string {
	return strings.Join([]string{
		harness.ManagedMarker,
		"AHT_INTEGRATION_ID=" + gooseIntegrationID,
		"AHT_INTEGRATION_VERSION=" + strconv.Itoa(harness.IntegrationVersion),
		"AHT_SOURCE=" + gooseIntegrationSource,
		"",
	}, "\n")
}

func goosePayloadDefaults(payload map[string]any) harness.PayloadDefaults {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "goose_event", harness.PayloadString(payload, "event"))
	harness.AddAttributeString(attributes, "goose_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "goose_tool_name", harness.PayloadString(payload, "tool_name"))
	harness.AddAttributeString(attributes, "goose_matcher_context", harness.PayloadString(payload, "matcher_context"))

	cwd := harness.PayloadString(payload, "working_dir")

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: "",
		CWD:         cwd,
		ProjectRoot: cwd,
		Event:       harness.PayloadString(payload, "event"),
		Attributes:  attributes,
	}
}

func goosePluginsDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".agents", "plugins")
	}

	return filepath.Join(".agents", "plugins")
}
