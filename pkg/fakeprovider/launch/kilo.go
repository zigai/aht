package launch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

const kiloModelName = "AHT fakeprovider"

func kiloPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	configDir := filepath.Join(paths.Root, "kilo")

	configJSON, err := kiloConfigJSON(model)
	if err != nil {
		return Plan{}, err
	}

	selected := ProviderName + "/" + model.Model

	return Plan{
		TitleRequests: []TitleRequest{
			{Role: "system", Prefix: "You are a title generator."},
		},
		Executable: "kilo",
		Protocol:   ProtocolOpenAIChat,
		Env:        []string{"KILO_CONFIG_DIR=" + configDir},
		Files: []File{{
			Path:   filepath.Join(configDir, "opencode.json"),
			Update: func([]byte) ([]byte, error) { return configJSON, nil },
		}},
		Headless: func(prompt string) ([]string, error) {
			return []string{"run", modelFlag, selected, "--auto", "--dir", paths.Work, prompt}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return []string{modelFlag, selected}, nil
			}

			return []string{modelFlag, selected, "--prompt", prompt}, nil
		},
		ShellTool: Tool{
			Name:      "bash",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func kiloConfigJSON(model Model) ([]byte, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			ProviderName: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": kiloModelName,
				"options": map[string]any{
					"baseURL": model.Endpoint + "/v1",
					"apiKey":  model.APIKey,
				},
				"models": map[string]any{model.Model: map[string]any{"name": kiloModelName}},
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode kilo config: %w", err)
	}

	return data, nil
}
