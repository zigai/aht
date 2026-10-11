package goose

import (
	"errors"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

var errEmptyPrompt = errors.New("goose headless run needs a prompt")

func (gooseHarness) LaunchPlan(_ harness.LaunchPaths, model harness.LaunchModel) (harness.LaunchPlan, error) {
	return harness.LaunchPlan{
		Executable: gooseCommand,
		Protocol:   fakeprovider.ProtocolOpenAIChat,
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
		Files:    nil,
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errEmptyPrompt
			}
			return []string{"run", "--text", prompt, "--provider", "openai", "--model", model.Model, "--max-turns", "2", "--quiet"}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return []string{"session", "--provider", "openai", "--model", model.Model}, nil
			}
			return []string{"run", "--text", prompt, "--interactive", "--provider", "openai", "--model", model.Model}, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "shell",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
