package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

const (
	agyCommandWaitMs = 5000
	agyDefaultModel  = "gemini-2.5-flash"
)

var errAgyEmptyPrompt = errors.New("agy headless run needs a prompt")

func agyPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	modelID := agyModelID(model.Model)

	return Plan{
		TitleRequests: []TitleRequest{{
			Role:   "system",
			Prefix: "You are a conversation title generator.",
		}},
		Executable: "agy",
		Protocol:   ProtocolGemini,
		Env: []string{
			"GEMINI_API_KEY=" + model.APIKey,
			"GOOGLE_GEMINI_BASE_URL=" + model.Endpoint,
		},
		Files: []File{{
			Path: filepath.Join(paths.Home, ".gemini", "antigravity-cli", "settings.json"),
			Update: func(existing []byte) ([]byte, error) {
				return agySettings(existing, paths.Work)
			},
		}},
		Headless: func(prompt string) ([]string, error) {
			if prompt == "" {
				return nil, errAgyEmptyPrompt
			}

			return []string{
				"-p",
				prompt,
				modelFlag,
				modelID,
				"--dangerously-skip-permissions",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{modelFlag, modelID}
			if prompt != "" {
				args = append(args, "-i", prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
			Name: "run_command",
			Arguments: func(command string) map[string]any {
				return map[string]any{
					"CommandLine":       command,
					"WaitMsBeforeAsync": agyCommandWaitMs,
					"toolSummary":       "Run shell command",
				}
			},
		},
	}, nil
}

func agyModelID(model string) string {
	if model == "" || model == defaultModel {
		return agyDefaultModel
	}

	return model
}

func agySettings(existing []byte, work string) ([]byte, error) {
	settings := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, fmt.Errorf("decode agy settings: %w", err)
		}
	}

	settings["modelProvider"] = "gemini"
	settings["onboardingComplete"] = true
	settings["consumerOnboardingComplete"] = true

	if work != "" {
		settings["trustedWorkspaces"] = appendUniqueString(settings["trustedWorkspaces"], work)
	}

	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode agy settings: %w", err)
	}

	return append(content, '\n'), nil
}

func appendUniqueString(existing any, value string) []any {
	list, _ := existing.([]any)

	out := make([]any, 0, len(list)+1)
	for _, item := range list {
		if text, ok := item.(string); ok && text == value {
			return list
		}

		out = append(out, item)
	}

	return append(out, value)
}
