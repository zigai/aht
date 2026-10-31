package droid

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const (
	droidLaunchModelID         = "custom:" + harness.LaunchProviderName + "-0"
	droidLaunchMaxOutputTokens = 4096
	droidShellTimeoutSeconds   = 5
)

func (droidHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	factoryDir := filepath.Join(paths.Home, ".factory")

	return harness.LaunchPlan{
		Executable: droidCommand,
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env: []string{
			"FACTORY_CONFIG_DIR=" + factoryDir,
			"FACTORY_DROID_AUTO_UPDATE_ENABLED=false",
			"FACTORY_AIRGAP_ENABLED=true",
			"FACTORY_API_KEY=" + model.APIKey,
		},
		Files: []harness.LaunchFile{{
			Path: filepath.Join(factoryDir, "settings.json"),
			Update: func(existing []byte) ([]byte, error) {
				trustRoot, err := filepath.EvalSymlinks(paths.Work)
				if err != nil {
					return nil, fmt.Errorf("resolve Droid trust root: %w", err)
				}

				return droidLaunchSettings(existing, model, trustRoot)
			},
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(string) ([]string, error) {
			return droidRPCArguments(paths.Work), nil
		},
		Interactive: func(prompt string) ([]string, error) {
			var args []string
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "Execute",
			Arguments: func(command string) map[string]any {
				return map[string]any{
					"command":         command,
					"timeout":         droidShellTimeoutSeconds,
					"riskLevel":       "low",
					"riskLevelReason": "Runs a command.",
				}
			},
		},
	}, nil
}

func droidRPCArguments(work string) []string {
	return []string{
		"exec",
		"--input-format",
		"stream-jsonrpc",
		"--output-format",
		"stream-jsonrpc",
		"--auto",
		"high",
		"--cwd",
		work,
	}
}

func droidLaunchSettings(
	existing []byte,
	model harness.LaunchModel,
	trustRoot string,
) ([]byte, error) {
	settings := make(map[string]any)
	if existing != nil {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("decode Droid settings: %w", err)
		}
	}

	settings["model"] = droidLaunchModelID
	trustedFolders := mapValue(settings, "trustedFolders")
	trustedFolders[trustRoot] = map[string]any{"trustedAt": time.Now().UTC().Format(time.RFC3339)}
	settings["customModels"] = []any{map[string]any{
		"model":           model.Model,
		"displayName":     harness.LaunchProviderName,
		"baseUrl":         model.Endpoint + "/v1",
		"apiKey":          model.APIKey,
		"provider":        "generic-chat-completion-api",
		"maxOutputTokens": droidLaunchMaxOutputTokens,
	}}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Droid settings: %w", err)
	}

	return data, nil
}

func mapValue(parent map[string]any, key string) map[string]any {
	value, ok := parent[key].(map[string]any)
	if ok {
		return value
	}

	value = make(map[string]any)
	parent[key] = value

	return value
}
