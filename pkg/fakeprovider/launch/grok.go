package launch

import (
	"fmt"
	"path/filepath"
)

func grokPlan(
	paths Paths,
	model Model,
) (Plan, error) {
	home := filepath.Join(paths.Root, "grok")
	config := fmt.Sprintf(
		"[model.%s]\nmodel = %q\nbase_url = %q\nname = \"AHT "+
			"compatibility\"\nenv_key = \"AHT_FAKEPROVIDER_API_KEY\"\napi_back"+
			"end = \"responses\"\n\n[models]\ndefault = "+
			"%q\n\n[ui]\nprompt_suggestions = false\n",
		ProviderName,
		model.Model,
		model.Endpoint+"/v1",
		ProviderName,
	)
	trusted := fmt.Sprintf("[folders.%q]\ntrusted = true\n", paths.Work)

	return Plan{
		TitleRequests: []TitleRequest{
			{Tool: "session_title"},
			{
				Role:     "user",
				Prefix:   "<system-reminder>",
				Contains: "Generate a session title for the conversation above.",
				Suffix:   "</system-reminder>",
			},
			{
				Role:     "user",
				Prefix:   "<system-reminder>",
				Contains: "Write an ultra-short dashboard line that captures the AGENT'S REPLY for the last turn only",
				Suffix:   "</system-reminder>",
			},
		},
		Executable: "grok",
		Protocol:   ProtocolOpenAIResponses,
		Env: []string{
			"GROK_HOME=" + home,
			"GROK_DISABLE_AUTOUPDATER=1",
			"AHT_FAKEPROVIDER_API_KEY=" + model.APIKey,
		},
		Files: []File{
			{
				Path:   filepath.Join(home, "config.toml"),
				Update: func([]byte) ([]byte, error) { return []byte(config), nil },
			},
			{
				Path:   filepath.Join(home, "trusted_folders.toml"),
				Update: func([]byte) ([]byte, error) { return []byte(trusted), nil },
			},
		},
		Headless: func(prompt string) ([]string, error) {
			return []string{
				"-p",
				prompt,
				modelFlag,
				ProviderName,
				"--always-approve",
				"--disable-web-search",
				"--no-memory",
				"--max-turns",
				"2",
			}, nil
		},
		Interactive: func(prompt string) ([]string, error) {
			args := []string{modelFlag, ProviderName}
			if prompt != "" {
				args = append(args, prompt)
			}

			return args, nil
		},
		ShellTool: Tool{
			Name: "run_terminal_command",
			Arguments: func(command string) map[string]any {
				return map[string]any{"command": command, "description": "Run a command"}
			},
		},
	}, nil
}
