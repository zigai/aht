package launch

import (
	"errors"
)

var errGooseEmptyPrompt = errors.New("goose headless run needs a prompt")

func goosePlan(
	_ Paths,
	model Model,
) (Plan, error) {
	return Plan{
		Executable: "goose",
		Protocol:   ProtocolOpenAIChat,
		Env: []string{
			"GOOSE_PROVIDER=openai",
			"GOOSE_MODEL=" + model.Model,
			"GOOSE_PROVIDER__TYPE=openai",
			"GOOSE_PROVIDER__HOST=" + model.Endpoint + "/v1",
			"GOOSE_PROVIDER__API_KEY=" + model.APIKey,
			"GOOSE_MODE=auto",
			"OPENAI_HOST=" + model.Endpoint,
			"OPENAI_API_KEY=" + model.APIKey,
			"OPENAI_BASE_PATH=v1/chat/completions",
			"GOOSE_DISABLE_SESSION_NAMING=true",
			"GOOSE_TELEMETRY_ENABLED=false",
		},
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errGooseEmptyPrompt
			}

			return []string{
				"run",
				"--text",
				prompt,
				"--provider",
				"openai",
				modelFlag,
				model.Model,
				"--max-turns",
				"2",
				"--quiet",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return []string{"session", "--provider", "openai", modelFlag, model.Model}, nil
			}

			return []string{
				"run",
				"--text",
				prompt,
				"--interactive",
				"--provider",
				"openai",
				modelFlag,
				model.Model,
			}, nil
		},
		ShellTool: Tool{
			Name: "shell",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
