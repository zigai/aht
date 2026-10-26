package opencode

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const launchModelName = "AHT fakeprovider"

func (opencodeHarness) LaunchPlan(paths harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	configDir := filepath.Join(paths.Root, "opencode")
	configJSON, err := launchConfigJSON(model)
	if err != nil {
		return harness.LaunchPlan{}, err
	}
	selected := harness.LaunchProviderName + "/" + model.Model

	return harness.LaunchPlan{
		Executable: "opencode",
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env:        []string{"OPENCODE_CONFIG_DIR=" + configDir},
		Files: []harness.LaunchFile{{
			Path:   filepath.Join(configDir, "opencode.json"),
			Update: func([]byte) ([]byte, error) { return configJSON, nil },
		}},
		Setup:    [][]string{{"service", "set", "disabled", "true"}, {"debug", "config"}},
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			return []string{"run", "--model", selected, "--auto", prompt}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, nil
			}
			return []string{"--prompt", prompt}, nil
		},
		ShellTool: harness.LaunchTool{
			Name:      "shell",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func launchConfigJSON(model harness.LaunchModel) ([]byte, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   harness.LaunchProviderName + "/" + model.Model,
		// Native V2 agents.<id>.disabled removes the auxiliary title agent:
		// https://opencode.ai/v2/docs/agents#disabled
		// Its background model call would consume the deterministic tool script
		// before the primary turn. This config is only for isolated fakeprovider
		// launches; normal installations keep native title generation enabled.
		"agents": map[string]any{"title": map[string]any{"disabled": true}},
		"provider": map[string]any{
			harness.LaunchProviderName: map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"name":    launchModelName,
				"options": map[string]any{"baseURL": model.Endpoint + "/v1", "apiKey": model.APIKey},
				"models":  map[string]any{model.Model: map[string]any{"name": launchModelName}},
			},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode opencode config: %w", err)
	}
	return data, nil
}
