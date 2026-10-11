package codex

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

var (
	errEmptyPrompt  = errors.New("codex headless run needs a prompt")
	errLaunchConfig = errors.New("codex config conflicts with the fake provider")
)

func (codexHarness) LaunchPlan(paths harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	home := filepath.Join(paths.Root, "codex")
	return harness.LaunchPlan{
		Executable: codexCommand,
		Protocol:   fakeprovider.ProtocolOpenAIResponses,
		Env: []string{
			"CODEX_HOME=" + home,
			"AHT_FAKEPROVIDER_API_KEY=" + model.APIKey,
		},
		Files: []harness.LaunchFile{{
			Path: filepath.Join(home, "config.toml"),
			Update: func(existing []byte) ([]byte, error) {
				return launchConfig(existing, model)
			},
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errEmptyPrompt
			}
			return []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--skip-git-repo-check", "-C", paths.Work, prompt}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{"-C", paths.Work, "-m", model.Model}
			if prompt != "" {
				args = append(args, prompt)
			}
			return args, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "exec_command",
			Arguments: func(command string) map[string]any {
				return map[string]any{"cmd": command}
			},
		},
	}, nil
}

func launchConfig(existing []byte, model harness.LaunchModel) ([]byte, error) {
	config := make(map[string]any)
	if err := toml.Unmarshal(existing, &config); err != nil {
		return nil, fmt.Errorf("decode codex config: %w", err)
	}
	providers := make(map[string]any)
	if value, ok := config["model_providers"]; ok {
		table, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: model_providers is %T", errLaunchConfig, value)
		}
		providers = table
	}
	providers[harness.LaunchProviderName] = map[string]any{
		"name":                 "AHT fakeprovider",
		"base_url":             model.Endpoint + "/v1",
		"env_key":              "AHT_FAKEPROVIDER_API_KEY",
		"wire_api":             "responses",
		"requires_openai_auth": false,
	}
	config["model"] = model.Model
	config["model_provider"] = harness.LaunchProviderName
	config["model_providers"] = providers
	content, err := toml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode codex config: %w", err)
	}
	return content, nil
}
