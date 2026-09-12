package kimi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	kimiCommand                     = "kimi"
	kimiSessionFlag                 = "--session"
	kimiCodeIntegrationSource       = "kimi-code-hook"
	kimiCodeManagedIntegrationStart = "# BEGIN aht managed integration: kimi-code"
	kimiCodeManagedIntegrationEnd   = "# END aht managed integration: kimi-code"
)

type kimiCodeHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID     string `json:"session_id"      validate:"required,notblank"`
	CWD           string `json:"cwd"             validate:"required,notblank"`
	HookEventName string `json:"hook_event_name" validate:"required,notblank"`
}

type kimiCodeHookSpec struct {
	event   string
	matcher string
	command string
	timeout int
}

func New() kimiCodeHarness {
	return kimiCodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:      registry.HarnessKimiCode,
		Aliases: []string{"kimi", "kimi_code", "kimicode"},
		// Native startup replaces argv with this title through setproctitle.
		ProcessNames: []string{"kimi", "kimi-code", "kimi_code", "kimicode", "kimi code"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: nil,
			PID:         nil,
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart: true,
			SessionEnd:   true,
			RunningIdle:  true,
			// Approval waiting is observed through aht wire kimi-code.
			WaitingPermission: true,
			NativeCatalog:     true,
			ProcessIdentity:   false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
	})}
}

func (kimiCodeHarness) InstallPlan(binary string) harness.InstallPlan {
	return harness.InstallPlan{Actions: []harness.InstallAction{harness.ManagedTextBlockAction{Plan: harness.ManagedTextBlockInstallPlan{
		Path:        filepath.Join(kimiCodeHome(), "config.toml"),
		Label:       "kimi-code hooks",
		ConfigLabel: "kimi-code config",
		StartMarker: kimiCodeManagedIntegrationStart,
		EndMarker:   kimiCodeManagedIntegrationEnd,
		Block:       kimiCodeHookBlock(binary),
	}}}}
}

func (kimiCodeHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{kimiCommand, kimiSessionFlag, sessionID}
}

func (kimiCodeHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (kimiCodeHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	return kimiCodePayloadDefaults(payload)
}

func kimiCodeHookBlock(binary string) string {
	specs := []kimiCodeHookSpec{
		{
			event:   harness.HookEventSessionStart,
			matcher: "startup|resume",
			command: kimiCodeHookCommand(binary, registry.ActivityIdle, harness.HookEventSessionStart),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   harness.HookEventUserPromptSubmit,
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, harness.HookEventUserPromptSubmit),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   harness.HookEventPreToolUse,
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, harness.HookEventPreToolUse),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   harness.HookEventPostToolUse,
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUse),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   harness.HookEventPostToolUseFailure,
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, harness.HookEventPostToolUseFailure),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   harness.HookEventStop,
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityIdle, harness.HookEventStop),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "StopFailure",
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityFailed, "StopFailure"),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "SubagentStart",
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, "SubagentStart"),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "SubagentStop",
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityIdle, "SubagentStop"),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "PreCompact",
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityRunning, "PreCompact"),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "PostCompact",
			matcher: "",
			command: kimiCodeHookCommand(binary, registry.ActivityIdle, "PostCompact"),
			timeout: harness.HookTimeoutSeconds,
		},
		{
			event:   "SessionEnd",
			matcher: "exit",
			command: kimiCodeHookCommand(binary, registry.PresenceGone, "SessionEnd"),
			timeout: harness.HookTimeoutSeconds,
		},
	}

	var builder strings.Builder
	builder.WriteString(kimiCodeManagedIntegrationStart)
	builder.WriteByte('\n')
	builder.WriteString("# ")
	builder.WriteString(harness.ManagedMarker)
	builder.WriteByte('\n')
	for _, spec := range specs {
		builder.WriteByte('\n')
		builder.WriteString("[[hooks]]\n")
		builder.WriteString("event = ")
		builder.WriteString(strconv.Quote(spec.event))
		builder.WriteByte('\n')
		if spec.matcher != "" {
			builder.WriteString("matcher = ")
			builder.WriteString(strconv.Quote(spec.matcher))
			builder.WriteByte('\n')
		}
		builder.WriteString("command = ")
		builder.WriteString(strconv.Quote(spec.command))
		builder.WriteByte('\n')
		builder.WriteString("timeout = ")
		builder.WriteString(strconv.Itoa(spec.timeout))
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
	builder.WriteString(kimiCodeManagedIntegrationEnd)
	builder.WriteByte('\n')

	return builder.String()
}

func kimiCodeHookCommand[T harness.Transition](binary string, transition T, event string) string {
	return harness.ReportHookCommand(binary, registry.HarnessKimiCode, transition, event, kimiCodeIntegrationSource)
}

func kimiCodePayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	sessionID := harness.PayloadString(payload, "session_id")
	sessionPath, err := kimiCodeSessionPath(sessionID)
	if err != nil {
		return harness.PayloadDefaults{}, err
	}
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "kimi_code_hook_event", harness.PayloadString(payload, "hook_event_name"))
	harness.AddAttributeString(attributes, "kimi_code_start_source", harness.PayloadString(payload, "source"))
	harness.AddAttributeString(attributes, "kimi_code_tool_name", harness.PayloadString(payload, "tool_name"))
	harness.AddAttributeString(attributes, "kimi_code_turn_id", payloadScalarString(payload, "turn_id"))
	harness.AddAttributeString(attributes, "kimi_code_decision", harness.PayloadString(payload, "decision"))
	harness.AddAttributeString(attributes, "kimi_code_reason", harness.PayloadString(payload, "reason"))
	harness.AddAttributeString(attributes, "kimi_code_notification_type", harness.PayloadStringAny(payload, "notification_type", "type"))

	return harness.PayloadDefaults{
		SessionID:   sessionID,
		SessionPath: sessionPath,
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func payloadScalarString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func kimiCodeSessionPath(sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return "", nil
	}

	sessionsRoot := filepath.Join(kimiCodeHome(), "sessions")
	workDirs, err := os.ReadDir(sessionsRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("reading Kimi Code sessions: %w", err)
	}
	for _, workDir := range workDirs {
		if !workDir.IsDir() {
			continue
		}
		sessionPath := filepath.Join(sessionsRoot, workDir.Name(), sessionID)
		info, statErr := os.Stat(sessionPath)
		if statErr == nil && info.IsDir() {
			return sessionPath, nil
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspecting Kimi Code session: %w", statErr)
		}
	}
	return "", nil
}

func kimiCodeHome() string {
	if value := strings.TrimSpace(os.Getenv("KIMI_SHARE_DIR")); value != "" {
		return value
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".kimi")
	}

	return ".kimi"
}
