package launch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

const opencodeModelName = "AHT fakeprovider"

func opencodePlan(
	paths Paths,
	model Model,
) (Plan, error) {
	configDir := filepath.Join(paths.Root, "opencode")

	configJSON, err := opencodeConfigJSON(model)
	if err != nil {
		return Plan{}, err
	}

	selected := ProviderName + "/" + model.Model

	return Plan{
		Executable: "opencode",
		Protocol:   ProtocolOpenAIChat,
		Env:        []string{"OPENCODE_CONFIG_DIR=" + configDir},
		Files: []File{{
			Path:   filepath.Join(configDir, "opencode.json"),
			Update: func([]byte) ([]byte, error) { return configJSON, nil },
		}},
		Setup: [][]string{{"service", "set", "disabled", "true"}, {"debug", "config"}},
		Headless: func(prompt string) ([]string, error) {
			return []string{"run", modelFlag, selected, "--auto", prompt}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, nil
			}

			return []string{"--prompt", prompt}, nil
		},
		ShellTool: Tool{
			Name:      "shell",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func opencodeConfigJSON(model Model) ([]byte, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   ProviderName + "/" + model.Model,
		// Native V2 agents.<id>.disabled removes the auxiliary title agent:
		// https://opencode.ai/v2/docs/agents#disabled
		// Its background model call would consume the deterministic tool script
		// before the primary turn. This config is only for isolated fakeprovider
		// launches; normal installations keep native title generation enabled.
		"agents": map[string]any{"title": map[string]any{"disabled": true}},
		"provider": map[string]any{
			ProviderName: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": opencodeModelName,
				"options": map[string]any{
					"baseURL": model.Endpoint + "/v1",
					"apiKey":  model.APIKey,
				},
				"models": map[string]any{model.Model: map[string]any{"name": opencodeModelName}},
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode opencode config: %w", err)
	}

	return data, nil
}
