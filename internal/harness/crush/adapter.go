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
	integrationVersion           = 16
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
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@charmland/crush",
			Directory: "crush",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: false, Question: false, UsageLimit: false},
			Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: false},
		},
		ScreenManifest:   screenManifest,
		ExclusiveProcess: true,
		ID:               registry.Harness("crush"),
		ProcessNames:     []string{"crush"},
		Env: harness.EnvKeys{
			Config:      []string{"CRUSH_GLOBAL_CONFIG", "CRUSH_GLOBAL_DATA", "CRUSH_SKILLS_DIR"},
			SessionID:   []string{"CRUSH_SESSION_ID"},
			ProjectRoot: []string{"CRUSH_PROJECT_DIR"},
			Event:       []string{"CRUSH_EVENT"},
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  crushIntegrationSource,
		StateAuthority:     registry.AuthorityScreen,
	})}
}

func (adapter crushHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	path := crushGlobalShellConfigPath()
	if path == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.ManagedTextBlockAction{Plan: harness.ManagedTextBlockInstallPlan{
				Path:        path,
				Label:       "crush hooks",
				ConfigLabel: "crush config",
				StartMarker: crushManagedIntegrationStart,
				EndMarker:   crushManagedIntegrationEnd,
				Block:       crushHookBlock(binary, adapter.Definition()),
			}},
		},
	}, nil
}

func crushHookBlock(binary string, definition harness.Definition) string {
	command := harness.ReportHookCommand(
		binary,
		definition,
		harness.ReportStdinRaw,
		registry.ActivityRunning,
		"PreToolUse",
	)

	return crushManagedIntegrationStart + "\n" + definition.Identity().Marker("# ") +
		"hook add " + "PreToolUse" +
		" --name " + crushHookName +
		" --timeout " + strconv.Itoa(
		harness.HookTimeoutSeconds,
	) +
		" --command " + harness.ShellQuote(
		command,
	) + "\n" +
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
	harness.AddAttributeString(
		attributes,
		"crush_hook_event",
		harness.PayloadString(payload, "event"),
	)
	harness.AddAttributeString(
		attributes,
		"crush_tool_name",
		harness.PayloadString(payload, "tool_name"),
	)

	return harness.PayloadDefaults{
		SessionID:  harness.PayloadString(payload, "session_id"),
		CWD:        harness.PayloadString(payload, "cwd"),
		Event:      harness.PayloadString(payload, "event"),
		Attributes: attributes,
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
