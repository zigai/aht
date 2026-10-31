package kilo

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const launchModelName = "AHT fakeprovider"

func (kiloHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	configDir := filepath.Join(paths.Root, "kilo")

	configJSON, err := launchConfigJSON(model)
	if err != nil {
		return harness.LaunchPlan{}, err
	}

	selected := harness.LaunchProviderName + "/" + model.Model

	return harness.LaunchPlan{
		Executable: "kilo",
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env:        []string{"KILO_CONFIG_DIR=" + configDir},
		Files: []harness.LaunchFile{{
			Path:   filepath.Join(configDir, "opencode.json"),
			Update: func([]byte) ([]byte, error) { return configJSON, nil },
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			return []string{"run", "--model", selected, "--auto", "--dir", paths.Work, prompt}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return []string{"--model", selected}, nil
			}

			return []string{"--model", selected, "--prompt", prompt}, nil
		},
		ShellTool: harness.LaunchTool{
			Name:      "bash",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func launchConfigJSON(model harness.LaunchModel) ([]byte, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			harness.LaunchProviderName: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": launchModelName,
				"options": map[string]any{
					"baseURL": model.Endpoint + "/v1",
					"apiKey":  model.APIKey,
				},
				"models": map[string]any{model.Model: map[string]any{"name": launchModelName}},
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode kilo config: %w", err)
	}

	return data, nil
}
