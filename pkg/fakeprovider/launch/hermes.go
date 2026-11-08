package launch

import (
	"errors"
	"fmt"
	"path/filepath"
)

const customProvider = "custom"

var errHermesInitialPrompt = errors.New(
	"hermes chat takes no initial prompt on the command line; use -q for a one-shot query",
)

func hermesPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	home := filepath.Join(paths.Root, "hermes")
	config := fmt.Sprintf(
		"model:\n  default: %q\n  provider: %s\n  base_url: %q\n  "+
			"api_key: %q\n  api_mode: chat_completions\napprovals:\n  mode: "+
			"manual\nplugins:\n  enabled:\n    - %s\n",
		model.Model,
		customProvider,
		model.Endpoint+"/v1",
		model.APIKey, "aht-state",
	)

	return Plan{
		TitleRequests: []TitleRequest{{Role: "system", Prefix: "You name chat sessions."}},
		Executable:    "hermes",
		Protocol:      ProtocolOpenAIChat,
		Env: []string{
			"HERMES_HOME=" + home,
			"OPENAI_API_KEY=" + model.APIKey,
		},
		Files: []File{{
			Path:   filepath.Join(home, "config.yaml"),
			Update: func([]byte) ([]byte, error) { return []byte(config), nil },
		}},
		Headless: func(prompt string) ([]string, error) {
			return []string{
				"-z",
				prompt,
				"--provider",
				customProvider,
				modelFlag,
				model.Model,
				"--yolo",
				"--accept-hooks",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt != "" {
				return nil, errHermesInitialPrompt
			}

			return []string{"chat", "--provider", customProvider, modelFlag, model.Model}, nil
		},
		ShellTool: Tool{
			Name: "terminal",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
