package launch

import (
	"errors"
	"fmt"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

const (
	ompModelName     = "AHT fakeprovider"
	ompContextWindow = 32000
	ompMaxTokens     = 4096
)

var errLaunchHookCount = errors.New("expected exactly one installed omp extension")

func ompPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	agentDir := filepath.Join(paths.Root, "pi-agent")

	modelsYAML, err := launchModelsYAML(model)
	if err != nil {
		return Plan{}, err
	}

	selected := ProviderName + "/" + model.Model

	return Plan{
		TitleRequests: []TitleRequest{
			{Role: "system", Prefix: "You name coding-agent sessions."},
			{Role: "user", Prefix: "<title-request>", Suffix: "</title-request>"},
		},
		Executable: "omp",
		Protocol:   ProtocolOpenAIChat,
		Env:        []string{"PI_CODING_AGENT_DIR=" + agentDir, "OMP_SKIP_SETUP=1"},
		Files: []File{{
			Path:   filepath.Join(agentDir, "models.yml"),
			Update: func([]byte) ([]byte, error) { return modelsYAML, nil },
		}},
		Headless: func(prompt string) ([]string, error) {
			hook, err := installedHook(agentDir)
			if err != nil {
				return nil, err
			}

			return []string{
				"-p",
				prompt,
				modelFlag,
				selected,
				"--auto-approve",
				"--max-time",
				"30s",
				"--hook",
				hook,
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			hook, err := installedHook(agentDir)
			if err != nil {
				return nil, err
			}

			return withPrompt([]string{modelFlag, selected, "--hook", hook}, prompt), nil
		},
		ShellTool: Tool{
			Name:      "bash",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

func installedHook(agentDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(agentDir, "extensions", "*"))
	if err != nil {
		return "", fmt.Errorf("find installed omp extension: %w", err)
	}

	if len(matches) != 1 {
		return "", fmt.Errorf("%w: found %d under %s", errLaunchHookCount, len(matches), agentDir)
	}

	return matches[0], nil
}

func launchModelsYAML(model Model) ([]byte, error) {
	config := map[string]any{
		"providers": map[string]any{
			ProviderName: map[string]any{
				"baseUrl": model.Endpoint + "/v1",
				"api":     "openai-completions",
				"apiKey":  model.APIKey,
				"models": []any{map[string]any{
					"id":            model.Model,
					"name":          ompModelName,
					"reasoning":     false,
					"input":         []string{"text"},
					"contextWindow": ompContextWindow,
					"maxTokens":     ompMaxTokens,
				}},
			},
		},
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode omp models: %w", err)
	}

	return data, nil
}
