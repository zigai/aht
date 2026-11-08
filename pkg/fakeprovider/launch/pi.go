package launch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

const (
	launchModelName     = "AHT fakeprovider"
	launchContextWindow = 32000
	launchMaxTokens     = 4096
)

func piPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	agentDir := filepath.Join(paths.Root, "pi-agent")

	modelsJSON, err := launchModelsJSON(model)
	if err != nil {
		return Plan{}, err
	}

	return Plan{
		Executable: "pi",
		Protocol:   ProtocolOpenAIChat,
		Env:        []string{"PI_CODING_AGENT_DIR=" + agentDir},
		Files: []File{{
			Path:   filepath.Join(agentDir, "models.json"),
			Update: func([]byte) ([]byte, error) { return modelsJSON, nil },
		}},
		Headless: func(string) ([]string, error) {
			return []string{
				"--mode",
				"rpc",
				"--provider",
				ProviderName,
				modelFlag,
				model.Model,
				"--approve",
				"--no-skills",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			return withPrompt(
				[]string{
					"--provider",
					ProviderName,
					modelFlag,
					model.Model,
					"--approve",
					"--no-skills",
				},
				prompt,
			), nil
		},
		ShellTool: Tool{
			Name:      "bash",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func launchModelsJSON(model Model) ([]byte, error) {
	config := map[string]any{
		"providers": map[string]any{
			ProviderName: map[string]any{
				"baseUrl": model.Endpoint + "/v1",
				"api":     "openai-completions",
				"apiKey":  model.APIKey,
				"compat": map[string]any{
					"supportsDeveloperRole":   false,
					"supportsReasoningEffort": false,
				},
				"models": []any{map[string]any{
					"id":            model.Model,
					"name":          launchModelName,
					"reasoning":     false,
					"input":         []string{"text"},
					"contextWindow": launchContextWindow,
					"maxTokens":     launchMaxTokens,
				}},
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode pi models: %w", err)
	}

	return data, nil
}

func withPrompt(args []string, prompt string) []string {
	if prompt == "" {
		return args
	}

	return append(args, "--", prompt)
}
