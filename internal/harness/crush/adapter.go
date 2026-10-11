package crush

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	crushCommand                 = "crush"
	crushIntegrationSource       = "crush-hook"
	crushHookName                = "aht"
	crushManagedIntegrationStart = "# BEGIN aht managed integration: crush"
	crushManagedIntegrationEnd   = "# END aht managed integration: crush"
)

type crushHarness struct{ harness.BaseAdapter }

type hookPayload struct {
	Event     string `json:"event"      validate:"required,notblank"`
	SessionID string `json:"session_id" validate:"required,notblank"`
	CWD       string `json:"cwd"        validate:"required,notblank"`
	ToolName  string `json:"tool_name"  validate:"required,notblank"`
}

func New() crushHarness {
	return crushHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: true,
		CatalogCreates:   false,
		ID:               registry.Harness("crush"),
		Aliases:          nil,
		ProcessNames:     []string{"crush"},
		Env: harness.EnvKeys{
			SessionID:   []string{"CRUSH_SESSION_ID"},
			SessionPath: nil,
			ProjectRoot: []string{"CRUSH_PROJECT_DIR"},
			PID:         nil,
			Event:       []string{"CRUSH_EVENT"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      false,
			SessionEnd:        false,
			RunningIdle:       false,
			WaitingPermission: false,
			ProcessIdentity:   false,
			NativeCatalog:     true,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: harness.IntegrationVersion,
		IntegrationSource:  crushIntegrationSource,
		StateAuthority:     registry.AuthorityScreen,
		ScreenFallback:     false,
	})}
}

func (crushHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	path := crushGlobalShellConfigPath()
	if path == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.ManagedTextBlockAction{Plan: harness.ManagedTextBlockInstallPlan{
		Path:        path,
		Label:       "crush hooks",
		ConfigLabel: "crush config",
		StartMarker: crushManagedIntegrationStart,
		EndMarker:   crushManagedIntegrationEnd,
		Block:       crushHookBlock(binary),
	}}}}, nil
}

func crushHookBlock(binary string) string {
	command := harness.ReportHookCommand(binary, registry.Harness("crush"), registry.ActivityRunning, harness.HookEventPreToolUse, crushIntegrationSource)

	return crushManagedIntegrationStart + "\n# " + harness.ManagedMarker + "\n# AHT_INTEGRATION_ID=crush\n" +
		"hook add " + harness.HookEventPreToolUse +
		" --name " + crushHookName +
		" --timeout " + strconv.Itoa(harness.HookTimeoutSeconds) +
		" --command " + harness.ShellQuote(command) + "\n" +
		crushManagedIntegrationEnd + "\n"
}

func (crushHarness) ResumeCommand(sessionID string, _ string) []string {
	if sessionID == "" {
		return nil
	}

	return []string{crushCommand, "--session", sessionID}
}

func (crushHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[hookPayload]()(rawPayload)
}

func (crushHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)
	harness.AddAttributeString(attributes, "crush_hook_event", harness.PayloadString(payload, "event"))
	harness.AddAttributeString(attributes, "crush_tool_name", harness.PayloadString(payload, "tool_name"))

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: "",
		CWD:         harness.PayloadString(payload, "cwd"),
		ProjectRoot: "",
		Event:       harness.PayloadString(payload, "event"),
		Attributes:  attributes,
	}, nil
}

func crushConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "crush")
	}
	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".config", "crush")
}

func crushGlobalConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CRUSH_GLOBAL_CONFIG")); value != "" {
		return value
	}

	return crushConfigDir()
}

func crushGlobalShellConfigPath() string {
	dir := crushGlobalConfigDir()
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, "crushrc")
}
