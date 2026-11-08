package harness

import "cmp"

// CommandHook is one native hook event that a generated command hook reports
// as a transition. A zero Stdin means ReportStdinRaw, and a zero Timeout means
// HookTimeoutSeconds.
//
//exhaustruct:optional
type CommandHook struct {
	Event      string
	Matcher    string
	Transition HookTransition
	Stdin      ReportStdin
	Timeout    int
}

// CommandHookPayload is the payload that Claude Code-style command hooks
// receive on stdin.
type CommandHookPayload struct {
	SessionID      string  `json:"session_id"      validate:"required,notblank"`
	TranscriptPath *string `json:"transcript_path" validate:"omitempty"`
	CWD            string  `json:"cwd"             validate:"required,notblank"`
	HookEventName  string  `json:"hook_event_name" validate:"required,notblank"`
}

// CommandHookSpecs renders a hook table into install specs that report for
// the harness the definition describes.
func CommandHookSpecs(
	binary string,
	definition Definition,
	hooks []CommandHook,
) []CommandHookInstallSpec {
	specs := make([]CommandHookInstallSpec, 0, len(hooks))
	for _, hook := range hooks {
		specs = append(specs, CommandHookInstallSpec{
			Event:   hook.Event,
			Matcher: hook.Matcher,
			Command: ReportHookCommand(
				binary,
				definition,
				cmp.Or(hook.Stdin, ReportStdinRaw),
				hook.Transition,
				hook.Event,
			),
			Timeout: hook.Timeout,
		})
	}

	return specs
}

func CommandHookGroup(
	command string,
	matcher string,
	statusMessage string,
	timeoutSeconds int,
) map[string]any {
	if timeoutSeconds <= 0 {
		timeoutSeconds = HookTimeoutSeconds
	}

	hook := map[string]any{
		"type":    HookTypeCommand,
		"command": command,
		"timeout": float64(timeoutSeconds),
	}
	if statusMessage != "" {
		hook["statusMessage"] = statusMessage
	}

	group := map[string]any{
		"hooks": []any{
			hook,
		},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}

	return group
}
