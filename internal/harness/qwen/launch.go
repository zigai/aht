package qwen

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

func (qwenHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	common := []string{"--auth-type", "openai", "--model", model.Model}

	return harness.LaunchPlan{
		Executable: "qwen",
		Protocol:   fakeprovider.ProtocolOpenAIChat,
		Env: []string{
			"OPENAI_API_KEY=" + model.APIKey,
			"OPENAI_BASE_URL=" + model.Endpoint + "/v1",
			"OPENAI_MODEL=" + model.Model,
			"QWEN_DISABLE_AUTO_TITLE=1",
		},
		Files: []harness.LaunchFile{{
			Path:   filepath.Join(paths.Home, ".qwen", "settings.json"),
			Update: launchSettings,
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			return append([]string{"-p", prompt, "--yolo"}, common...), nil
		},
		Interactive: func(prompt string) ([]string, error) {
			if prompt == "" {
				return common, nil
			}

			return append([]string{"-i", prompt}, common...), nil
		},
		ShellTool: harness.LaunchTool{
			Name:      "run_shell_command",
			Arguments: func(command string) map[string]any { return map[string]any{"command": command} },
		},
	}, nil
}

// Qwen Code spends extra model requests on background memory, skill review and
// followup suggestions after a turn; the scripted provider answers exactly the
// turn's requests.
func launchSettings(existing []byte) ([]byte, error) {
	settings := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("decode qwen settings: %w", err)
		}
	}

	settings["memory"] = map[string]any{
		"enableManagedAutoMemory": false,
		"enableManagedAutoDream":  false,
		"enableAutoSkill":         false,
	}
	settings["ui"] = map[string]any{"enableFollowupSuggestions": false}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode qwen settings: %w", err)
	}

	return data, nil
}
