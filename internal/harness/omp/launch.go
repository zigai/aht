package omp

import (
	"errors"
	"fmt"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const (
	launchModelName     = "AHT fakeprovider"
	launchContextWindow = 32000
	launchMaxTokens     = 4096
)

var errLaunchHookCount = errors.New("expected exactly one installed omp extension")

func (ompHarness) LaunchPlan(paths harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	agentDir := filepath.Join(paths.Root, "pi-agent")
	modelsYAML, err := launchModelsYAML(model)
	if err != nil {
		return harness.LaunchPlan{}, err
	}
	selected := harness.LaunchProviderName + "/" + model.Model

	return harness.LaunchPlan{
		Executable: "omp",
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env:        []string{"PI_CODING_AGENT_DIR=" + agentDir, "OMP_SKIP_SETUP=1"},
		Files: []harness.LaunchFile{{
			Path:   filepath.Join(agentDir, "models.yml"),
			Update: func([]byte) ([]byte, error) { return modelsYAML, nil },
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			hook, err := installedHook(agentDir)
			if err != nil {
				return nil, err
			}
			return []string{"-p", prompt, "--model", selected, "--auto-approve", "--max-time", "30s", "--hook", hook}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			hook, err := installedHook(agentDir)
			if err != nil {
				return nil, err
			}
			return withPrompt([]string{"--model", selected, "--hook", hook}, prompt), nil
		},
		ShellTool: harness.LaunchTool{
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

func launchModelsYAML(model harness.LaunchModel) ([]byte, error) {
	config := map[string]any{
		"providers": map[string]any{
			harness.LaunchProviderName: map[string]any{
				"baseUrl": model.Endpoint + "/v1",
				"api":     "openai-completions",
				"apiKey":  model.APIKey,
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
	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode omp models: %w", err)
	}
	return data, nil
}

func withPrompt(args []string, prompt string) []string {
	if prompt == "" {
		return args
	}
	return append(args, "--", prompt)
}
