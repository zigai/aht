//go:build compatibility

package hostcompat

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

const compatibilityPrompt = "Use the available shell tool exactly once to run printf aht-compat-marker, then reply done."

func lifecycleToolName(id registry.Harness) string {
	if id == registry.HarnessClaude {
		return "Bash"
	}
	if id == registry.HarnessCodex {
		return "exec_command"
	}
	if id == registry.HarnessCopilot {
		return "bash"
	}
	if id == registry.HarnessCline {
		return "run_commands"
	}
	if id == registry.HarnessKimiCode {
		return "Shell"
	}
	if id == registry.HarnessGrok {
		return "run_terminal_command"
	}
	if id == registry.HarnessDroid {
		return "Execute"
	}
	if id == registry.HarnessGoose {
		return "shell"
	}
	if id == registry.HarnessOpenCode || id == registry.HarnessKilo {
		return "bash"
	}
	if id == registry.HarnessOpenClaw {
		return "exec"
	}
	if id == registry.HarnessPi || id == registry.HarnessOmp {
		return "bash"
	}
	if id == registry.HarnessHermes {
		return "terminal"
	}
	return "shell"
}

func lifecycleToolArgs(id registry.Harness) map[string]any {
	if id == registry.HarnessCodex {
		return map[string]any{"cmd": "printf aht-compat-marker"}
	}
	if id == registry.HarnessKimiCode {
		return map[string]any{"command": "printf 'aht-compat-marker\\n'"}
	}
	if id == registry.HarnessCline {
		return map[string]any{"commands": []any{map[string]any{"command": "printf aht-compat-marker"}}}
	}
	if id == registry.HarnessGrok || id == registry.HarnessCopilot {
		return map[string]any{"command": "printf aht-compat-marker", "description": "Print the compatibility marker"}
	}
	if id == registry.HarnessDroid {
		return map[string]any{"command": "printf aht-compat-marker", "timeout": 5, "riskLevel": "low", "riskLevelReason": "Prints only the fixed compatibility marker."}
	}
	return map[string]any{"command": "printf aht-compat-marker"}
}

func (host isolatedHost) lifecycleCommand(t *testing.T) (*exec.Cmd, []*exec.Cmd) {
	t.Helper()

	env := append([]string{}, host.env...)
	baseURL := host.provider.URL() + "/v1"
	var args []string
	var setup []*exec.Cmd
	switch host.contract.ID {
	case registry.HarnessClaude:
		env = append(env, "ANTHROPIC_BASE_URL="+host.provider.URL(), "ANTHROPIC_AUTH_TOKEN=compat", "ANTHROPIC_API_KEY=compat")
		args = []string{"-p", compatibilityPrompt, "--model", "compat", "--allowedTools", "Bash", "--dangerously-skip-permissions"}
	case registry.HarnessCodex:
		config := fmt.Sprintf("model = \"compat\"\nmodel_provider = \"compat\"\n[model_providers.compat]\nname = \"AHT compatibility\"\nbase_url = %q\nenv_key = \"AHT_COMPAT_API_KEY\"\nwire_api = \"responses\"\nrequires_openai_auth = false\n", baseURL)
		host.writeFile(t, filepath.Join(host.root, "codex", "config.toml"), config)
		env = append(env, "AHT_COMPAT_API_KEY=compat")
		args = []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--skip-git-repo-check", "-C", host.work, compatibilityPrompt}
	case registry.HarnessGrok:
		config := fmt.Sprintf("[model.aht-compat]\nmodel = \"compat\"\nbase_url = %q\nname = \"AHT compatibility\"\nenv_key = \"AHT_COMPAT_API_KEY\"\napi_backend = \"responses\"\n\n[models]\ndefault = \"aht-compat\"\n", baseURL)
		host.writeFile(t, filepath.Join(host.root, "grok", "config.toml"), config)
		env = append(env, "AHT_COMPAT_API_KEY=compat")
		args = []string{"-p", compatibilityPrompt, "--model", "aht-compat", "--always-approve", "--disable-web-search", "--no-memory", "--max-turns", "2"}
	case registry.HarnessCopilot:
		env = append(env, "COPILOT_PROVIDER_BASE_URL="+baseURL, "COPILOT_PROVIDER_TYPE=openai", "COPILOT_PROVIDER_API_KEY=compat", "COPILOT_MODEL=gpt-4", "COPILOT_PROVIDER_WIRE_MODEL=compat", "COPILOT_ALLOW_ALL=1")
		args = []string{"-p", compatibilityPrompt, "--allow-all", "--no-auto-update", "--stream", "off"}
	case registry.HarnessCline:
		configDir := filepath.Join(host.root, "cline")
		dataDir := filepath.Join(host.root, "cline-data")
		setup = append(setup, host.command(env, "auth", "-p", "openai-compatible", "-k", "compat", "-m", "compat", "-b", baseURL, "--config", configDir, "--data-dir", dataDir))
		args = []string{"--config", configDir, "--data-dir", dataDir, "--provider", "openai-compatible", "--key", "compat", "--model", "compat", "--auto-approve", "true", compatibilityPrompt}
	case registry.HarnessPi:
		host.writeFile(t, filepath.Join(host.root, "pi-agent", "models.json"), piModelsJSON(baseURL))
		args = []string{"--mode", "rpc", "--provider", "aht-compat", "--model", "compat", "--approve"}
	case registry.HarnessKimiCode:
		host.configureKimiModel(t, baseURL)
		return host.kimiWireCommand(t, env, []string{"--yolo", "--no-thinking", "--model", "aht-compat", "--max-steps-per-turn", "2"}), nil
	case registry.HarnessOmp:
		host.writeFile(t, filepath.Join(host.root, "pi-agent", "models.yml"), ompModelsYAML(baseURL))
		hooks, err := filepath.Glob(filepath.Join(host.root, "pi-agent", "extensions", "*"))
		if err != nil || len(hooks) != 1 {
			t.Fatalf("locating installed OMP hook: paths=%v error=%v", hooks, err)
		}
		args = []string{"-p", compatibilityPrompt, "--model", "aht-compat/compat", "--auto-approve", "--max-time", "30s", "--hook", hooks[0]}
	case registry.HarnessGoose:
		env = append(env,
			"GOOSE_PROVIDER=openai",
			"GOOSE_MODEL=compat",
			"GOOSE_PROVIDER__TYPE=openai",
			"GOOSE_PROVIDER__HOST="+baseURL,
			"GOOSE_PROVIDER__API_KEY=compat",
			"GOOSE_MODE=auto",
			"OPENAI_HOST="+host.provider.URL(),
			"OPENAI_API_KEY=compat",
			"OPENAI_BASE_PATH=v1/chat/completions",
			"GOOSE_DISABLE_SESSION_NAMING=true",
			"GOOSE_TELEMETRY_ENABLED=false",
		)
		args = []string{"run", "--text", compatibilityPrompt, "--provider", "openai", "--model", "compat", "--max-turns", "2", "--quiet"}
	case registry.HarnessOpenCode, registry.HarnessKilo:
		configDir := filepath.Join(host.root, string(host.contract.ID))
		host.writeFile(t, filepath.Join(configDir, "opencode.json"), opencodeConfigJSON(baseURL))
		args = []string{"run", "--model", "aht-compat/compat", "--auto", "--dir", host.work, compatibilityPrompt}
	case registry.HarnessHermes:
		config := fmt.Sprintf("model:\n  default: compat\n  provider: custom\n  base_url: %q\n  api_key: compat\n  api_mode: chat_completions\napprovals:\n  mode: manual\nplugins:\n  enabled:\n    - aht-state\n", baseURL)
		host.writeFile(t, filepath.Join(host.root, "hermes", "config.yaml"), config)
		env = append(env, "OPENAI_API_KEY=compat")
		args = []string{"-z", compatibilityPrompt, "--provider", "custom", "--model", "compat", "--yolo", "--accept-hooks"}
	case registry.HarnessOpenClaw:
		port := host.configureOpenClawModel(t, baseURL)
		host.startOpenClawGateway(t, port)
		args = []string{"agent", "--session-id", "aht-compat", "--model", "aht-compat/compat", "--message", compatibilityPrompt, "--timeout", "30"}
	case registry.HarnessDroid:
		host.configureDroidModel(t, baseURL)
		env = append(env, "FACTORY_API_KEY=compat")
		args = droidRPCArguments(host.work)
	default:
		t.Fatalf("%s is marked lifecycle without a driver", host.contract.ID)
	}
	return host.command(env, args...), setup
}

func (host isolatedHost) command(env []string, args ...string) *exec.Cmd {
	command := exec.Command(host.hostPath, args...)
	command.Env = env
	command.Dir = host.work
	return command
}
