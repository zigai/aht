package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

const claudeKeySuffixLength = 20

var errClaudeEmptyPrompt = errors.New("claude headless run needs a prompt")

func claudePlan(
	paths Paths,
	model Model,
) (Plan, error) {
	configDir := filepath.Join(paths.Root, "claude")

	return Plan{
		TitleRequests: []TitleRequest{
			{
				Role:   "system",
				Prefix: "You are naming a coding session so the user can pick it out of a long list of sessions.",
			},
		},
		Executable: "claude",
		Protocol:   ProtocolAnthropicMessages,
		Env: []string{
			"CLAUDE_CONFIG_DIR=" + configDir,
			"ANTHROPIC_BASE_URL=" + model.Endpoint,
			"ANTHROPIC_AUTH_TOKEN=" + model.APIKey,
			"ANTHROPIC_API_KEY=" + model.APIKey,
			"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false",
		},
		Files: []File{{
			Path: filepath.Join(configDir, ".claude.json"),
			Update: func(existing []byte) ([]byte, error) {
				return launchState(existing, paths.Work, model.APIKey)
			},
		}},
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errClaudeEmptyPrompt
			}

			return []string{
				"-p",
				prompt,
				modelFlag,
				model.Model,
				"--allowedTools",
				"Bash",
				"--dangerously-skip-permissions",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{modelFlag, model.Model}
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
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
