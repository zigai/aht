package launch

import (
	"errors"
	"fmt"
	"path/filepath"
)

var (
	errKimiConfigMissing = errors.New("kimi-code config.toml is missing")
	errKimiInitialPrompt = errors.New(
		"kimi-code interactive sessions take no initial prompt on the command line",
	)
)

func kimiPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	home := filepath.Join(paths.Root, "kimi")
	provider := ProviderName
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

	return Plan{
		Executable: "kimi",
		Protocol:   ProtocolOpenAIResponses,
		Env:        []string{"KIMI_CODE_HOME=" + home},
		Files: []File{{
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

			return []string{modelFlag, provider}, nil
		},
		ShellTool: Tool{
			Name: "Bash",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
