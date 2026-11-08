package launch

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

var (
	errCodexEmptyPrompt  = errors.New("codex headless run needs a prompt")
	errCodexLaunchConfig = errors.New("codex config conflicts with the fake provider")
)

func codexPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	home := filepath.Join(paths.Root, "codex")

	return Plan{
		Executable: "codex",
		Protocol:   ProtocolOpenAIResponses,
		Env: []string{
			"CODEX_HOME=" + home,
			"AHT_FAKEPROVIDER_API_KEY=" + model.APIKey,
		},
		Files: []File{{
			Path: filepath.Join(home, "config.toml"),
			Update: func(existing []byte) ([]byte, error) {
				return codexConfig(existing, model)
			},
		}},
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errCodexEmptyPrompt
			}

			return []string{
				"exec",
				"--dangerously-bypass-approvals-and-sandbox",
				"--dangerously-bypass-hook-trust",
				"--skip-git-repo-check",
				"-C",
				paths.Work,
				prompt,
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{"-C", paths.Work, "-m", model.Model}
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
			Name: "exec_command",
			Arguments: func(command string) map[string]any {
				return map[string]any{"cmd": command}
			},
		},
	}, nil
}

func codexConfig(existing []byte, model Model) ([]byte, error) {
	config := make(map[string]any)
	if err := toml.Unmarshal(existing, &config); err != nil {
		return nil, fmt.Errorf("decode codex config: %w", err)
	}

	providers := make(map[string]any)

	if value, ok := config["model_providers"]; ok {
		table, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: model_providers is %T", errCodexLaunchConfig, value)
		}

		providers = table
	}

	providers[ProviderName] = map[string]any{
		"name":                 "AHT fakeprovider",
		"base_url":             model.Endpoint + "/v1",
		"env_key":              "AHT_FAKEPROVIDER_API_KEY",
		"wire_api":             "responses",
		"requires_openai_auth": false,
	}
	config["model"] = model.Model
	config["model_provider"] = ProviderName
	config["model_providers"] = providers

	content, err := toml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode codex config: %w", err)
	}

	return content, nil
}
