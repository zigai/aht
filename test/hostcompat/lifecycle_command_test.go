//go:build compatibility

package hostcompat

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

const compatibilityPrompt = "Use the available shell tool exactly once to run printf aht-compat-marker, then reply done."

var lifecycleToolNames = map[registry.Harness]string{
	registry.Harness("claude"):    "Bash",
	registry.Harness("codex"):     "exec_command",
	registry.Harness("copilot"):   "bash",
	registry.Harness("cline"):     "run_commands",
	registry.Harness("kimi-code"): "Bash",
	registry.Harness("grok"):      "run_terminal_command",
	registry.Harness("droid"):     "Execute",
	registry.Harness("goose"):     "shell",
	registry.Harness("opencode"):  "bash",
	registry.Harness("kilo"):      "bash",
	registry.Harness("openclaw"):  "exec",
	registry.Harness("pi"):        "bash",
	registry.Harness("omp"):       "bash",
	registry.Harness("hermes"):    "terminal",
	registry.Harness("qwen"):      "run_shell_command",
}

func lifecycleToolName(id registry.Harness) string {
	if name, ok := lifecycleToolNames[id]; ok {
		return name
	}
	return "shell"
}

func lifecycleToolArgs(id registry.Harness) map[string]any {
	if id == registry.Harness("codex") {
		return map[string]any{"cmd": "printf aht-compat-marker"}
	}
	if id == registry.Harness("kimi-code") {
		return map[string]any{"command": "printf 'aht-compat-marker\\n'"}
	}
	if id == registry.Harness("cline") {
		return map[string]any{"commands": []any{map[string]any{"command": "printf aht-compat-marker"}}}
	}
	if id == registry.Harness("grok") || id == registry.Harness("copilot") {
		return map[string]any{"command": "printf aht-compat-marker", "description": "Print the compatibility marker"}
	}
	if id == registry.Harness("droid") {
		return map[string]any{"command": "printf aht-compat-marker", "timeout": 5, "riskLevel": "low", "riskLevelReason": "Prints only the fixed compatibility marker."}
	}
	return map[string]any{"command": "printf aht-compat-marker"}
}

type lifecycleLaunch struct {
	env   []string
	args  []string
	setup []*exec.Cmd
}

type lifecycleLauncher func(host *isolatedHost, t *testing.T, env []string, baseURL string) lifecycleLaunch

var lifecycleLaunchers = map[registry.Harness]lifecycleLauncher{
	registry.Harness("claude"):   (*isolatedHost).claudeLaunch,
	registry.Harness("codex"):    (*isolatedHost).codexLaunch,
	registry.Harness("grok"):     (*isolatedHost).grokLaunch,
	registry.Harness("copilot"):  (*isolatedHost).copilotLaunch,
	registry.Harness("cline"):    (*isolatedHost).clineLaunch,
	registry.Harness("pi"):       (*isolatedHost).piLaunch,
	registry.Harness("omp"):      (*isolatedHost).ompLaunch,
	registry.Harness("goose"):    (*isolatedHost).gooseLaunch,
	registry.Harness("opencode"): (*isolatedHost).opencodeLaunch,
	registry.Harness("kilo"):     (*isolatedHost).opencodeLaunch,
	registry.Harness("hermes"):   (*isolatedHost).hermesLaunch,
	registry.Harness("openclaw"): (*isolatedHost).openclawLaunch,
	registry.Harness("droid"):    (*isolatedHost).droidLaunch,
	registry.Harness("qwen"):     (*isolatedHost).qwenLaunch,
}

func (host *isolatedHost) lifecycleCommand(t *testing.T) (*exec.Cmd, []*exec.Cmd) {
	t.Helper()

	env := append([]string{}, host.env...)
	baseURL := host.provider.URL() + "/v1"
	if host.contract.ID == registry.Harness("kimi-code") {
		host.configureKimiModel(t, baseURL)
		return host.command(t, env, "acp"), nil
	}
	launcher, ok := lifecycleLaunchers[host.contract.ID]
	if !ok {
		t.Fatalf("%s is marked lifecycle without a driver", host.contract.ID)
	}
	launch := launcher(host, t, env, baseURL)
	return host.command(t, launch.env, launch.args...), launch.setup
}

func (host *isolatedHost) claudeLaunch(_ *testing.T, env []string, _ string) lifecycleLaunch {
	env = append(env, "ANTHROPIC_BASE_URL="+host.provider.URL(), "ANTHROPIC_AUTH_TOKEN=compat", "ANTHROPIC_API_KEY=compat")
	return lifecycleLaunch{env: env, args: []string{"-p", compatibilityPrompt, "--model", "compat", "--allowedTools", "Bash", "--dangerously-skip-permissions"}}
}

func (host *isolatedHost) codexLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	config := fmt.Sprintf("model = \"compat\"\nmodel_provider = \"compat\"\n[model_providers.compat]\nname = \"AHT compatibility\"\nbase_url = %q\nenv_key = \"AHT_COMPAT_API_KEY\"\nwire_api = \"responses\"\nrequires_openai_auth = false\n", baseURL)
	host.writeFile(t, filepath.Join(host.root, "codex", "config.toml"), config)
	env = append(env, "AHT_COMPAT_API_KEY=compat")
	return lifecycleLaunch{env: env, args: []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--skip-git-repo-check", "-C", host.work, compatibilityPrompt}}
}

func (host *isolatedHost) grokLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	config := fmt.Sprintf("[model.aht-compat]\nmodel = \"compat\"\nbase_url = %q\nname = \"AHT compatibility\"\nenv_key = \"AHT_COMPAT_API_KEY\"\napi_backend = \"responses\"\n\n[models]\ndefault = \"aht-compat\"\n", baseURL)
	host.writeFile(t, filepath.Join(host.root, "grok", "config.toml"), config)
	env = append(env, "AHT_COMPAT_API_KEY=compat")
	return lifecycleLaunch{env: env, args: []string{"-p", compatibilityPrompt, "--model", "aht-compat", "--always-approve", "--disable-web-search", "--no-memory", "--max-turns", "2"}}
}

func (*isolatedHost) copilotLaunch(_ *testing.T, env []string, baseURL string) lifecycleLaunch {
	env = append(env, "COPILOT_PROVIDER_BASE_URL="+baseURL, "COPILOT_PROVIDER_TYPE=openai", "COPILOT_PROVIDER_API_KEY=compat", "COPILOT_MODEL=gpt-4", "COPILOT_PROVIDER_WIRE_MODEL=compat", "COPILOT_ALLOW_ALL=1")
	return lifecycleLaunch{env: env, args: []string{"-p", compatibilityPrompt, "--allow-all", "--no-auto-update", "--stream", "off"}}
}

func (host *isolatedHost) clineLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	configDir := filepath.Join(host.root, "cline")
	dataDir := filepath.Join(host.root, "cline-data")
	setup := []*exec.Cmd{host.command(t, env, "auth", "-p", "openai-compatible", "-k", "compat", "-m", "compat", "-b", baseURL, "--config", configDir, "--data-dir", dataDir)}
	return lifecycleLaunch{env: env, args: []string{"--config", configDir, "--data-dir", dataDir, "--provider", "openai-compatible", "--key", "compat", "--model", "compat", "--auto-approve", "true", compatibilityPrompt}, setup: setup}
}

func (host *isolatedHost) piLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	host.writeFile(t, filepath.Join(host.root, "pi-agent", "models.json"), piModelsJSON(baseURL))
	return lifecycleLaunch{env: env, args: []string{"--mode", "rpc", "--provider", "aht-compat", "--model", "compat", "--approve"}}
}

func (host *isolatedHost) ompLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	host.writeFile(t, filepath.Join(host.root, "pi-agent", "models.yml"), ompModelsYAML(baseURL))
	hooks, err := filepath.Glob(filepath.Join(host.root, "pi-agent", "extensions", "*"))
	if err != nil || len(hooks) != 1 {
		t.Fatalf("locating installed OMP hook: paths=%v error=%v", hooks, err)
	}
	return lifecycleLaunch{env: env, args: []string{"-p", compatibilityPrompt, "--model", "aht-compat/compat", "--auto-approve", "--max-time", "30s", "--hook", hooks[0]}}
}

func (host *isolatedHost) gooseLaunch(_ *testing.T, env []string, baseURL string) lifecycleLaunch {
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
	return lifecycleLaunch{env: env, args: []string{"run", "--text", compatibilityPrompt, "--provider", "openai", "--model", "compat", "--max-turns", "2", "--quiet"}}
}

func (host *isolatedHost) opencodeLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	configDir := filepath.Join(host.root, string(host.contract.ID))
	host.writeFile(t, filepath.Join(configDir, "opencode.json"), opencodeConfigJSON(baseURL))
	if host.contract.ID == registry.Harness("opencode") {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		output, err := compatibilityOutput(ctx, host.command(t, env, "debug", "config"))
		cancel()
		if err != nil {
			t.Fatalf("%s native configuration and plugin dependency setup failed: %v\n%s", host.contract.ID, err, output)
		}
	}
	return lifecycleLaunch{env: env, args: []string{"run", "--model", "aht-compat/compat", "--auto", "--dir", host.work, compatibilityPrompt}}
}

func (host *isolatedHost) hermesLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	config := fmt.Sprintf("model:\n  default: compat\n  provider: custom\n  base_url: %q\n  api_key: compat\n  api_mode: chat_completions\napprovals:\n  mode: manual\nplugins:\n  enabled:\n    - aht-state\n", baseURL)
	host.writeFile(t, filepath.Join(host.root, "hermes", "config.yaml"), config)
	env = append(env, "OPENAI_API_KEY=compat")
	return lifecycleLaunch{env: env, args: []string{"-z", compatibilityPrompt, "--provider", "custom", "--model", "compat", "--yolo", "--accept-hooks"}}
}

func (host *isolatedHost) openclawLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	port := host.configureOpenClawModel(t, baseURL)
	host.startOpenClawGateway(t, port)
	return lifecycleLaunch{env: env, args: []string{"agent", "--session-id", "aht-compat", "--model", "aht-compat/compat", "--message", compatibilityPrompt, "--timeout", "30"}}
}

func (host *isolatedHost) droidLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	host.configureDroidModel(t, baseURL)
	env = append(env, "FACTORY_API_KEY=compat")
	return lifecycleLaunch{env: env, args: droidRPCArguments(host.work)}
}

func (host *isolatedHost) qwenLaunch(t *testing.T, env []string, baseURL string) lifecycleLaunch {
	t.Helper()
	host.configureQwenModel(t)
	env = append(env, "OPENAI_API_KEY=compat", "OPENAI_BASE_URL="+baseURL, "OPENAI_MODEL=compat", "QWEN_DISABLE_AUTO_TITLE=1")
	return lifecycleLaunch{env: env, args: []string{"-i", compatibilityPrompt, "--auth-type", "openai", "--model", "compat", "--yolo"}}
}

func (host *isolatedHost) command(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(t.Context(), host.hostPath, args...)
	command.Env = env
	command.Dir = host.work
	return command
}
