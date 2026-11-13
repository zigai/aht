package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

var (
	errCopilotEmptyPrompt  = errors.New("copilot headless run needs a prompt")
	errCopilotLaunchConfig = errors.New("copilot config conflicts with the launch trust list")
)

func copilotPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	return Plan{
		Executable: "copilot",
		Protocol:   ProtocolOpenAIChat,
		Env: []string{
			"COPILOT_HOME=" + filepath.Join(paths.Root, "copilot"),
			"COPILOT_PROVIDER_BASE_URL=" + model.Endpoint + "/v1",
			"COPILOT_PROVIDER_TYPE=openai",
			"COPILOT_PROVIDER_API_KEY=" + model.APIKey,
			"COPILOT_MODEL=gpt-4.1",
			"COPILOT_PROVIDER_WIRE_MODEL=" + model.Model,
		},
		Files: []File{{
			Path: filepath.Join(paths.Root, "copilot", "config.json"),
			Update: func(existing []byte) ([]byte, error) {
				return copilotConfig(existing, paths.Work)
			},
		}},
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errCopilotEmptyPrompt
			}

			return []string{"-p", prompt, "--allow-all", "--no-auto-update", "--stream", "off"}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{"--no-auto-update"}
			if prompt != "" {
				args = append(args, "-i", prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
			Name: "bash",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command, "description": "Run a command"}
			},
		},
	}, nil
}

func copilotConfig(existing []byte, work string) ([]byte, error) {
	config := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &config); err != nil {
			return nil, fmt.Errorf("decode copilot config: %w", err)
		}
	}

	folders := []any{}

	if value, ok := config["trusted_folders"]; ok {
		list, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: trusted_folders is %T", errCopilotLaunchConfig, value)
		}

		folders = list
	}

	if !slices.Contains(folders, any(work)) {
		folders = append(folders, work)
	}

	config["trusted_folders"] = folders

	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode copilot config: %w", err)
	}

	return append(content, '\n'), nil
}
