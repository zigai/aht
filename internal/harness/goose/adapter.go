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
}

type goosePayload struct {
	SessionID string `json:"session_id" validate:"notblank"`
}

func New() gooseHarness {
	return gooseHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("goose"),
		Aliases:          nil,
		ProcessNames:     []string{"goose"},
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
		StateAuthority:     registry.AuthorityHook,
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
	}, nil
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
		{event: harness.HookEventSessionStart, transition: harness.HookActivityIdle},
		{event: harness.HookEventUserPromptSubmit, transition: harness.HookActivityRunning},
		{event: harness.HookEventPreToolUse, transition: harness.HookActivityRunning},
		{event: harness.HookEventPostToolUse, transition: harness.HookActivityRunning},
		{event: harness.HookEventPostToolUseFailure, transition: harness.HookActivityRunning},
		{event: "BeforeReadFile", transition: harness.HookActivityRunning},
		{event: "AfterFileEdit", transition: harness.HookActivityRunning},
		{event: "BeforeShellExecution", transition: harness.HookActivityRunning},
		{event: "AfterShellExecution", transition: harness.HookActivityRunning},
		{event: harness.HookEventStop, transition: harness.HookActivityIdle},
		{event: "SessionEnd", transition: harness.HookPresenceGone},
	}
}

func gooseHookRule(spec gooseHookSpec) map[string]any {
	return map[string]any{
		"hooks": []any{
			gooseCommandHook(spec),
		},
	}
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
		"  " + harness.ShellQuote(binary) + " report " + harness.ShellQuote(string(registry.Harness("goose"))) + " --presence \"$transition\" --event \"$event\" --reporter-version " + harness.ShellQuote(strconv.Itoa(harness.IntegrationVersion)) + " --reporter " + harness.ShellQuote(gooseIntegrationSource) + " --raw-stdin-defaults-only --quiet >/dev/null 2>&1 || true",
		"else",
		"  " + harness.ShellQuote(binary) + " report " + harness.ShellQuote(string(registry.Harness("goose"))) + " --activity \"$transition\" --event \"$event\" --reporter-version " + harness.ShellQuote(strconv.Itoa(harness.IntegrationVersion)) + " --reporter " + harness.ShellQuote(gooseIntegrationSource) + " --raw-stdin-defaults-only --quiet >/dev/null 2>&1 || true",
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

func goosePluginsDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".agents", "plugins")
	}

	return filepath.Join(".agents", "plugins")
}

func (gooseHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["goose_event"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "goose_start_source", "source", "reason"))
}
