package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const claudeKeySuffixLength = 20

var errEmptyPrompt = errors.New("claude headless run needs a prompt")

func (claudeHarness) LaunchPlan(
	paths harness.LaunchPaths,
	model harness.LaunchModel,
) (harness.LaunchPlan, error) {
	configDir := filepath.Join(paths.Root, "claude")

	return harness.LaunchPlan{
		Executable: claudeCommand,
		Protocol:   fakeprovider.ProtocolAnthropicMessages,
		Env: []string{
			"CLAUDE_CONFIG_DIR=" + configDir,
			"ANTHROPIC_BASE_URL=" + model.Endpoint,
			"ANTHROPIC_AUTH_TOKEN=" + model.APIKey,
			"ANTHROPIC_API_KEY=" + model.APIKey,
			"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false",
		},
		Files: []harness.LaunchFile{{
			Path: filepath.Join(configDir, ".claude.json"),
			Update: func(existing []byte) ([]byte, error) {
				return launchState(existing, paths.Work, model.APIKey)
			},
		}},
		Setup:    nil,
		Services: nil,
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errEmptyPrompt
			}

			return []string{
				"-p",
				prompt,
				"--model",
				model.Model,
				"--allowedTools",
				"Bash",
				"--dangerously-skip-permissions",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{"--model", model.Model}
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: harness.LaunchTool{
			Name: "Bash",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command}
			},
		},
	}, nil
}

func launchState(existing []byte, work string, apiKey string) ([]byte, error) {
	state := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &state); err != nil {
			return nil, fmt.Errorf("decode claude state: %w", err)
		}
	}

	projects, ok := state["projects"].(map[string]any)
	if !ok {
		projects = make(map[string]any)
	}

	project, ok := projects[work].(map[string]any)
	if !ok {
		project = make(map[string]any)
	}

	project["hasTrustDialogAccepted"] = true
	projects[work] = project
	state["projects"] = projects
	state["hasCompletedOnboarding"] = true
	state["theme"] = "dark"

	approved := apiKey
	if len(approved) > claudeKeySuffixLength {
		approved = approved[len(approved)-claudeKeySuffixLength:]
	}

	state["customApiKeyResponses"] = map[string]any{
		"approved": []string{approved},
		"rejected": []string{},
	}

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode claude state: %w", err)
	}

	return append(content, '\n'), nil
}
