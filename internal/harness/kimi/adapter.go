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
	kimiCodeIntegrationVersion      = 16
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

func New() kimiCodeHarness {
	return kimiCodeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@moonshot-ai/kimi-code",
			Directory: "kimi",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
		},
		Env:          harness.EnvKeys{Config: []string{"KIMI_CODE_HOME"}},
		ID:           registry.Harness("kimi-code"),
		Aliases:      []string{"kimi", "kimi_code", "kimicode"},
		ProcessNames: []string{"kimi", "kimi-code"},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "kimi_code_hook_event_name",
		LifecycleSourceAttributes: []string{"kimi_code_source", "source", "reason"},
		IntegrationVersion:        kimiCodeIntegrationVersion,
		IntegrationSource:         kimiCodeIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
	})}
}

func (adapter kimiCodeHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := kimiCodeHome()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.ManagedTextBlockAction{Plan: harness.ManagedTextBlockInstallPlan{
				Path:        filepath.Join(base, "config.toml"),
				Label:       "kimi-code hooks",
				ConfigLabel: "kimi-code config",
				StartMarker: kimiCodeManagedIntegrationStart,
				EndMarker:   kimiCodeManagedIntegrationEnd,
				Block:       kimiCodeHookBlock(binary, adapter.Definition()),
			}},
		},
	}, nil
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
	for _, field := range []string{
		"hook_event_name",
		"source",
		"client_type",
		"session_title",
		"tool_name",
		"tool_call_id",
		"agent_id",
		"decision",
		"reason",
		"error_type",
	} {
		harness.AddAttributeString(
			attributes,
			"kimi_code_"+field,
			harness.PayloadString(payload, field),
		)
	}

	harness.AddAttributeString(
		attributes,
		"kimi_code_turn_id",
		payloadScalarString(payload, "turn_id"),
	)

	return harness.PayloadDefaults{
		SessionID:   sessionID,
		SessionPath: sessionPath,
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func kimiCodeHookBlock(binary string, definition harness.Definition) string {
	specs := harness.CommandHookSpecs(binary, definition, []harness.CommandHook{
		{Event: "SessionStart", Matcher: "startup|resume", Transition: harness.HookActivityIdle},
		{Event: "SessionHeartbeat", Transition: harness.HookPresenceLive},
		{Event: "UserPromptSubmit", Transition: harness.HookActivityRunning},
		{Event: "TurnStarted", Transition: harness.HookActivityRunning},
		{Event: "PreToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUse", Transition: harness.HookActivityRunning},
		{Event: "PostToolUseFailure", Transition: harness.HookActivityRunning},
		{Event: "PermissionRequest", Transition: harness.HookActivityWaiting},
		{Event: "PermissionResult", Transition: harness.HookActivityRunning},
		{Event: "Stop", Transition: harness.HookActivityIdle},
		{Event: "Interrupt", Transition: harness.HookActivityInterrupted},
		{Event: "StopFailure", Transition: harness.HookActivityFailed},
		{Event: "PreCompact", Transition: harness.HookActivityRunning},
		{Event: "PostCompact", Matcher: "^manual", Transition: harness.HookActivityIdle},
		{Event: "SessionEnd", Matcher: "exit|archive", Transition: harness.HookPresenceGone},
	})

	var builder strings.Builder
	builder.WriteString(kimiCodeManagedIntegrationStart + "\n" + definition.Identity().Marker("# "))

	for _, spec := range specs {
		builder.WriteString("\n[[hooks]]\nevent = " + tomlQuoteString(spec.Event) + "\n")

		if spec.Matcher != "" {
			builder.WriteString("matcher = " + tomlQuoteString(spec.Matcher) + "\n")
		}

		builder.WriteString("command = " + tomlQuoteString(spec.Command) + "\n")
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
	if !kimiCodeSessionDirectoryName(sessionID) {
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

func kimiCodeSessionDirectoryName(sessionID string) bool {
	return sessionID != "" && sessionID != "." && sessionID != ".." &&
		filepath.Base(sessionID) == sessionID
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
