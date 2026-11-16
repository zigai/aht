package claude

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

const (
	marketplaceName = "aht"
	pluginName      = "aht-state"
	pluginID        = pluginName + "@" + marketplaceName
)

//go:embed assets/register.ts.tmpl
var moduleTemplate string

// reportHooks is what the plugin reports for each Claude Code hook event.
var reportHooks = []harness.CommandHook{
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
	{Event: "PreToolUse", Transition: harness.HookActivityRunning},
	{Event: "PostToolUse", Transition: harness.HookActivityRunning},
	{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
	{Event: "PermissionRequest", Transition: harness.HookActivityWaiting},
	{Event: "Elicitation", Transition: harness.HookActivityWaiting},
	{Event: "ElicitationResult", Transition: harness.HookActivityRunning},
	{Event: "PermissionDenied", Transition: harness.HookActivityIdle},
	{
		Event:      "Notification",
		Matcher:    "permission_prompt",
		Transition: harness.HookActivityWaiting,
	},
	// SubagentStart and SubagentStop describe child agents, including
	// Claude Code's internal prompt-suggestion agents, not the main
	// turn. Stop reports whether delegated work keeps the turn running.
	// Automatic compaction either happens inside a turn that already
	// runs or between turns, where no Stop would return the session to
	// idle, so only manual compaction reports running.
	{Event: "PreCompact", Matcher: "manual", Transition: harness.HookActivityRunning},
	// Automatic compaction continues the running turn; only manual
	// compaction returns the session to idle.
	{Event: "PostCompact", Matcher: "manual", Transition: harness.HookActivityIdle},
	{Event: "Stop", Transition: harness.HookActivityIdle},
	{Event: "StopFailure", Transition: harness.HookActivityFailed},
	{Event: "SessionEnd", Transition: harness.HookPresenceGone},
}

// titleHookEvents refresh the cached session title through HandleHook.
var titleHookEvents = []string{"SessionStart", "Stop"}

type moduleCommand struct {
	Event   string   `json:"event"`
	Matcher string   `json:"matcher"`
	Args    []string `json:"args"`
}

func renderModule(binary string, definition harness.Definition) (string, error) {
	commands := make([]moduleCommand, 0, len(reportHooks)+len(titleHookEvents))
	for _, hook := range reportHooks {
		commands = append(commands, moduleCommand{
			Event:   hook.Event,
			Matcher: hook.Matcher,
			Args: harness.ReportHookArgs(
				definition,
				harness.ReportStdinRaw,
				hook.Transition,
				hook.Event,
			),
		})
	}

	for _, event := range titleHookEvents {
		commands = append(commands, moduleCommand{
			Event:   event,
			Matcher: "",
			Args:    harness.NativeHookArgs(definition.ID, event),
		})
	}

	encoded, err := json.Marshal(commands)
	if err != nil {
		return "", fmt.Errorf("encode claude plugin commands: %w", err)
	}

	// Claude Code reads each hook's event name from a string literal.
	hooks := make([]string, 0, len(commands))
	for _, command := range commands {
		hook := fmt.Sprintf(
			"  on(%q, async ($, e, next) => {\n    await report($, %q, e);\n    return next(e);\n  });",
			"classic."+command.Event,
			command.Event,
		)
		if !slices.Contains(hooks, hook) {
			hooks = append(hooks, hook)
		}
	}

	return strings.NewReplacer(
		"{{COMMANDS}}",
		string(encoded),
		"{{TIMEOUT_MS}}",
		strconv.FormatInt((harness.HookTimeoutSeconds*time.Second).Milliseconds(), 10),
		"{{HOOKS}}",
		strings.Join(hooks, "\n"),
	).Replace(harness.RenderScriptTemplate(moduleTemplate, binary, definition)), nil
}
