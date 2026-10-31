package hermes

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const customProvider = "custom"

var errHermesInitialPrompt = errors.New(
	"hermes chat takes no initial prompt on the command line; use -q for a one-shot query",
)

func (hermesHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	home := filepath.Join(paths.Root, "hermes")
	config := fmt.Sprintf(
		"model:\n  default: %q\n  provider: %s\n  base_url: %q\n  "+
			"api_key: %q\n  api_mode: chat_completions\napprovals:\n  mode: "+
			"manual\nplugins:\n  enabled:\n    - %s\n",
		model.Model,
		customProvider,
		model.Endpoint+"/v1",
		model.APIKey,
		hermesPluginName,
	)

	return harness.LaunchPlan{
		Executable: hermesCommand,
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env: []string{
			"HERMES_HOME=" + home,
			"OPENAI_API_KEY=" + model.APIKey,
		},
		Setup:    nil,
		Services: nil,
		Files: []harness.LaunchFile{{
			Path:   filepath.Join(home, "config.yaml"),
			Update: func([]byte) ([]byte, error) { return []byte(config), nil },
		}},
		Headless: func(prompt string) ([]string, error) {
			return []string{
				"-z",
				prompt,
				"--provider",
				customProvider,
				"--model",
				model.Model,
				"--yolo",
				"--accept-hooks",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt != "" {
				return nil, errHermesInitialPrompt
			}

			return []string{"chat", "--provider", customProvider, "--model", model.Model}, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "terminal",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}
