package claude

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
	claudeCommand           = "claude"
	claudeIntegrationSource = "claude-hook"
	integrationVersion      = 16
)

type claudeHarness struct{ harness.BaseAdapter }

func New() claudeHarness {
	return claudeHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		Distribution: harness.Distribution{
			Source:    harness.DistributionNPM,
			Package:   "@anthropic-ai/claude-code",
			Directory: "claude",
		},
		DetailCapabilities: registry.DetailCapabilities{
			Native: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true},
			Screen: registry.DetailSupport{Permission: true, Question: true, UsageLimit: true},
		},
		ScreenManifest:   screenManifest,
		PayloadOmissions: []string{"tool_input", "tool_response"},
		ExclusiveProcess: true, CatalogCreates: true,
		RetainNativeActivity: true,
		ID:                   registry.Harness("claude"),
		Aliases:              []string{"claude-code", "claude_code"},
		ProcessNames:         []string{"claude", "claude-code"},
		Env: harness.EnvKeys{
			Config:      []string{"CLAUDE_CONFIG_DIR"},
			SessionID:   []string{"CLAUDE_SESSION_ID"},
			SessionPath: []string{"CLAUDE_SESSION_PATH"},
			PID:         []string{"CLAUDE_PID"},
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
		},
		LifecycleEventAttribute:   "claude_hook_event",
		LifecycleSourceAttributes: []string{"claude_start_source", "source", "reason"},
		IntegrationVersion:        integrationVersion,
		IntegrationSource:         claudeIntegrationSource,
		StateAuthority:            registry.AuthorityHook,
		ProgramStatusApp:          "claude-code",
		HeldTurnEvents:            []string{"Stop"},
		ScreenFallback:            true,
	})}
}

func (adapter claudeHarness) InstallPlan(binary string) (harness.InstallPlan, error) {
	base := claudeConfigDir()
	if base == "" {
		return harness.InstallPlan{}, harness.ErrHomeUnknown
	}

	definition := adapter.Definition()
	marketplaceDir := filepath.Join(base, marketplaceName)
	version := "0.0." + strconv.Itoa(integrationVersion)
	description := "Reports Claude Code session state to aht"

	module, err := renderModule(binary, definition)
	if err != nil {
		return harness.InstallPlan{}, err
	}

	return harness.InstallPlan{
		Actions: []harness.InstallAction{
			harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
				Dir:   marketplaceDir,
				Label: "claude plugin",
				Files: []harness.RenderedFileInstallSpec{
					{Name: ".claude-plugin/marketplace.json", JSONContent: map[string]any{
						"name":  marketplaceName,
						"owner": map[string]any{"name": "aht"},
						"plugins": []any{map[string]any{
							"name":        pluginName,
							"source":      "./" + pluginName,
							"description": description,
							"version":     version,
						}},
					}},
					{Name: pluginName + "/.claude-plugin/plugin.json", JSONContent: map[string]any{
						"name":        pluginName,
						"version":     version,
						"description": description,
					}},
					{
						Name:        pluginName + "/hooks/hooks.json",
						JSONContent: map[string]any{"modules": []string{"./register.ts"}},
					},
					{Name: pluginName + "/hooks/register.ts", Content: module},
				},
				Identity: definition.Identity(),
				Registration: registration{
					settingsPath: filepath.Join(base, "settings.json"),
				},
				UpgradeRegisters: true,
			}},
		},
	}, nil
}

func (claudeHarness) PayloadCompatible(rawPayload json.RawMessage) bool {
	return harness.PayloadValidator[harness.CommandHookPayload]()(rawPayload)
}

func (claudeHarness) PayloadDefaults(payload map[string]any) (harness.PayloadDefaults, error) {
	attributes := make(map[string]string)

	switch kind := harness.PayloadString(payload, "error"); kind {
	case "rate_limit", "billing_error":
		attributes["claude_error_type"] = kind
	}

	harness.AddAttributeString(
		attributes,
		"claude_hook_event",
		harness.PayloadString(payload, "hook_event_name"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_start_source",
		harness.PayloadString(payload, "source"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_permission_mode",
		harness.PayloadString(payload, "permission_mode"),
	)
	harness.AddAttributeString(attributes, "claude_model", harness.PayloadString(payload, "model"))
	harness.AddAttributeString(
		attributes,
		"claude_notification_type",
		harness.PayloadStringAny(payload, "notification_type", "type"),
	)
	harness.AddAttributeString(
		attributes,
		"claude_session_end_reason",
		harness.PayloadString(payload, "reason"),
	)

	return harness.PayloadDefaults{
		SessionID:   harness.PayloadString(payload, "session_id"),
		SessionPath: harness.PayloadString(payload, "transcript_path"),
		CWD:         harness.PayloadString(payload, "cwd"),
		Event:       harness.PayloadString(payload, "hook_event_name"),
		Attributes:  attributes,
	}, nil
}

func claudeConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		return value
	}

	home := harness.HomeDir()
	if home == "" {
		return ""
	}

	return filepath.Join(home, ".claude")
}
