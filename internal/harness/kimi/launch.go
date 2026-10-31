package kimi

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

var (
	errKimiConfigMissing = errors.New("kimi-code config.toml is missing")
	errKimiInitialPrompt = errors.New(
		"kimi-code interactive sessions take no initial prompt on the command line",
	)
)

func (kimiCodeHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	home := filepath.Join(paths.Root, "kimi")
	provider := harness.LaunchProviderName
	header := fmt.Sprintf(`default_model = %[1]q
telemetry = false
auto_session_title = false

[providers.%[1]s]
type = "openai_responses"
base_url = %[2]q
api_key = %[3]q

[models.%[1]s]
provider = %[1]q
model = %[4]q
max_context_size = 128000
capabilities = ["tool_use"]

`, provider, model.Endpoint+"/v1", model.APIKey, model.Model)

	return harness.LaunchPlan{
		Executable: kimiCommand,
		Protocol:   fakeprovider.ProtocolOpenAIResponses,
		Env:        []string{"KIMI_CODE_HOME=" + home},
		Setup:      nil,
		Services:   nil,
		Files: []harness.LaunchFile{{
			Path: filepath.Join(home, "config.toml"),
			Update: func(existing []byte) ([]byte, error) {
				if existing == nil {
					return nil, errKimiConfigMissing
				}

				return append([]byte(header), existing...), nil
			},
		}},
		Headless: func(string) ([]string, error) {
			return []string{"acp"}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt != "" {
				return nil, errKimiInitialPrompt
			}

			return []string{"--model", provider}, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "Bash",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
