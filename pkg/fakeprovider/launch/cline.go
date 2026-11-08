package launch

import (
	"path/filepath"
)

const openAICompatibleProvider = "openai-compatible"

func clinePlan(
	paths Paths,
	model Model,
) (Plan, error) {
	configDir := filepath.Join(paths.Root, "cline")
	dataDir := filepath.Join(paths.Root, "cline-data")

	return Plan{
		Executable: "cline",
		Protocol:   ProtocolOpenAIChat,
		Env: []string{
			"CLINE_DIR=" + configDir,
			"CLINE_DATA_DIR=" + dataDir,
			"CLINE_DISABLE_CLINE_PASS_NOTICE=1",
		},
		Setup: [][]string{{
			"auth", "-p", openAICompatibleProvider, "-k", model.APIKey, "-m", model.Model,
			"-b", model.Endpoint + "/v1", "--config", configDir, "--data-dir", dataDir,
		}},
		Headless: func(prompt string) ([]string, error) {
			return []string{
				"--config",
				configDir,
				"--data-dir",
				dataDir,
				"--provider",
				openAICompatibleProvider,
				"--key",
				model.APIKey,
				modelFlag,
				model.Model,
				"--auto-approve",
				"true",
				prompt,
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{
				"--tui",
				"--config",
				configDir,
				"--data-dir",
				dataDir,
				"--provider",
				openAICompatibleProvider,
				"--key",
				model.APIKey,
				modelFlag,
				model.Model,
			}
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
			Name: "run_commands",
			Arguments: func(command string) map[string]any {
				return map[string]any{"commands": []any{map[string]any{"command": command}}}
			},
		},
	}, nil
}
