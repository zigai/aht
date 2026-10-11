package kimi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	kimiCommand                     = "kimi"
	kimiSessionFlag                 = "--session"
	kimiCodeIntegrationSource       = "kimi-code-hook"
	kimiCodeIntegrationVersion      = 15
	kimiCodeManagedIntegrationStart = "# BEGIN aht managed integration: kimi-code"
	kimiCodeManagedIntegrationEnd   = "# END aht managed integration: kimi-code"
)

type kimiCodeHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	SessionID     string `json:"session_id"      validate:"required,notblank"`
	CWD           string `json:"cwd"             validate:"required,notblank"`
	HookEventName string `json:"hook_event_name" validate:"required,notblank"`
	ClientType    string `json:"client_type"     validate:"required,eq=kimi_code_cli"`
}

type kimiCodeHookSpec struct {
	event      string
	matcher    string
	transition harness.HookTransition
}

func New() kimiCodeHarness {
	return kimiCodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: false,
		CatalogCreates:   false,
		ID:               registry.Harness("kimi-code"),
		Aliases:          []string{"kimi", "kimi_code", "kimicode"},
		ProcessNames:     []string{"kimi", "kimi-code"},
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
			WaitingPermission: true,
			NativeCatalog:     true,
			ProcessIdentity:   false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: kimiCodeIntegrationVersion,
		IntegrationSource:  kimiCodeIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (kimiCodeHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := kimiCodeHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.ManagedTextBlockAction{Plan: harness.ManagedTextBlockInstallPlan{
		Path:        filepath.Join(base, "config.toml"),
		Label:       "kimi-code hooks",
		ConfigLabel: "kimi-code config",
		StartMarker: kimiCodeManagedIntegrationStart,
		EndMarker:   kimiCodeManagedIntegrationEnd,
		Block:       kimiCodeHookBlock(binary),
	}}}}, nil
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
	sessionID := harness.PayloadString(payload, "session_id")
	sessionPath, err := kimiCodeSessionPath(sessionID)
	if err != nil {
		return harness.PayloadDefaults{}, err
	}
	attributes := make(map[string]string)
	for _, field := range []string{"hook_event_name", "source", "client_type", "session_title", "tool_name", "tool_call_id", "agent_id", "decision", "reason", "error_type"} {
		harness.AddAttributeString(attributes, "kimi_code_"+field, harness.PayloadString(payload, field))
	}
	harness.AddAttributeString(attributes, "kimi_code_turn_id", payloadScalarString(payload, "turn_id"))

	return harness.PayloadDefaults{
		SessionID:   sessionID,
		SessionPath: sessionPath,
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func kimiCodeHookBlock(binary string) string {
	specs := []kimiCodeHookSpec{
		{event: "SessionStart", matcher: "startup|resume", transition: harness.HookActivityIdle},
		{event: "SessionHeartbeat", matcher: "", transition: harness.HookPresenceLive},
		{event: "UserPromptSubmit", matcher: "", transition: harness.HookActivityRunning},
		{event: "TurnStarted", matcher: "", transition: harness.HookActivityRunning},
		{event: "PreToolUse", matcher: "", transition: harness.HookActivityRunning},
		{event: "PostToolUse", matcher: "", transition: harness.HookActivityRunning},
		{event: "PostToolUseFailure", matcher: "", transition: harness.HookActivityRunning},
		{event: "PermissionRequest", matcher: "", transition: harness.HookActivityWaiting},
		{event: "PermissionResult", matcher: "", transition: harness.HookActivityRunning},
		{event: "Stop", matcher: "", transition: harness.HookActivityIdle},
		{event: "Interrupt", matcher: "", transition: harness.HookActivityInterrupted},
		{event: "StopFailure", matcher: "", transition: harness.HookActivityFailed},
		{event: "PreCompact", matcher: "", transition: harness.HookActivityRunning},
		{event: "PostCompact", matcher: "^manual", transition: harness.HookActivityIdle},
		{event: "SessionEnd", matcher: "exit|archive", transition: harness.HookPresenceGone},
	}

	var builder strings.Builder
	builder.WriteString(kimiCodeManagedIntegrationStart + "\n# " + harness.ManagedMarker + "\n# AHT_INTEGRATION_ID=kimi-code\n")
	for _, spec := range specs {
		builder.WriteString("\n[[hooks]]\nevent = " + tomlQuoteString(spec.event) + "\n")
		if spec.matcher != "" {
			builder.WriteString("matcher = " + tomlQuoteString(spec.matcher) + "\n")
		}
		builder.WriteString("command = " + tomlQuoteString(kimiCodeHookCommand(binary, spec)) + "\n")
		builder.WriteString("timeout = " + strconv.Itoa(harness.HookTimeoutSeconds) + "\n")
	}
	builder.WriteString("\n" + kimiCodeManagedIntegrationEnd + "\n")
	return builder.String()
}

func tomlQuoteString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("encode TOML string: %w", err))
	}
	encoded = bytes.ReplaceAll(encoded, []byte{0x7f}, []byte(`\u007f`))
	return string(encoded)
}

func kimiCodeHookCommand(binary string, spec kimiCodeHookSpec) string {
	dimension, _, _ := strings.Cut(string(spec.transition), ":")
	return strings.Join([]string{
		harness.ShellQuote(binary), "report", "kimi-code",
		"--" + dimension, spec.transition.State(), "--event", spec.event,
		"--reporter-version", strconv.Itoa(kimiCodeIntegrationVersion),
		"--reporter", kimiCodeIntegrationSource, "--multi-session", "--raw-stdin", "--quiet",
	}, " ")
}

func payloadScalarString(payload map[string]any, key string) string {
	switch value := payload[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	default:
		return ""
	}
}

func kimiCodeSessionPath(sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return "", nil
	}
	home := kimiCodeHome()
	if home == "" {
		return "", nil
	}

	sessionsRoot := filepath.Join(home, "sessions")
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
	if value := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); value != "" {
		return value
	}
	home := harness.HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".kimi-code")
}

func (kimiCodeHarness) LifecycleDefaults(event string, attributes map[string]string) harness.LifecycleDefaults {
	if event == "" {
		event = attributes["kimi_code_hook_event_name"]
	}
	return harness.TranslateLifecycle(event, harness.FirstAttribute(attributes, "kimi_code_source", "source", "reason"))
}
