//go:build compatibility

package hostcompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

const compatibilityPrompt = "Use the available shell tool exactly once to run printf aht-compat-marker, then reply done."

type isolatedHost struct {
	root     string
	home     string
	work     string
	store    string
	aht      string
	hostPath string
	env      []string
	contract hostContract
	provider *scriptedProvider
}

func TestCurrentHarnessLifecycle(t *testing.T) {
	harnessID := registry.Harness(strings.TrimSpace(os.Getenv("AHT_COMPAT_HARNESS")))
	if harnessID == "" {
		t.Fatal("AHT_COMPAT_HARNESS is required; use `just compatibility <harness>`")
	}
	contract, err := contractFor(harnessID)
	if err != nil {
		t.Fatal(err)
	}
	host := newIsolatedHost(t, contract)
	host.installIntegration(t)
	host.assertIntegrationCurrent(t)
	host.assertVersion(t)
	if contract.Level == compatibilityDiscovery {
		t.Logf("%s current-host coverage is discovery-only: the documented CLI has no isolated local-provider lifecycle route", contract.ID)
		return
	}
	host.runLifecycle(t)
}

func newIsolatedHost(t *testing.T, contract hostContract) isolatedHost {
	t.Helper()

	hostPath, err := exec.LookPath(contract.Executable)
	if err != nil {
		t.Fatalf("current %s executable %q is not installed: %v", contract.ID, contract.Executable, err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	for _, directory := range []string{home, work} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	aht := filepath.Join(root, "bin", "aht-compat-oracle")
	if err := os.MkdirAll(filepath.Dir(aht), 0o700); err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(sourceDirectory(), "..", ".."))
	build := exec.Command("go", "build", "-ldflags", "-X github.com/zigai/aht/internal/cli.version=compat-oracle", "-o", aht, ".")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building compatibility oracle: %v\n%s", err, output)
	}

	env := append([]string{}, os.Environ()...)
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"AHT_STATE_DIR="+filepath.Join(root, "aht-state"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"),
		"CODEX_HOME="+filepath.Join(root, "codex"),
		"COPILOT_HOME="+filepath.Join(root, "copilot"),
		"AHT_STORE="+filepath.Join(root, "sessions.json"),
		"CLINE_DIR="+filepath.Join(root, "cline"),
		"CLINE_DATA_DIR="+filepath.Join(root, "cline-data"),
		"KIMI_SHARE_DIR="+filepath.Join(root, "kimi"),
		"GROK_HOME="+filepath.Join(root, "grok"),
		"PI_CODING_AGENT_DIR="+filepath.Join(root, "pi-agent"),
		"OPENCODE_CONFIG_DIR="+filepath.Join(root, "opencode"),
		"KILO_CONFIG_DIR="+filepath.Join(root, "kilo"),
		"AGY_CONFIG_HOME="+filepath.Join(root, "agy"),
		"FACTORY_CONFIG_DIR="+filepath.Join(root, "droid"),
		"HERMES_HOME="+filepath.Join(root, "hermes"),
	)

	return isolatedHost{
		root: root, home: home, work: work, store: filepath.Join(root, "sessions.json"),
		aht: aht, hostPath: hostPath, env: env, contract: contract, provider: nil,
	}
}

func sourceDirectory() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("locating compatibility source directory")
	}
	return filepath.Dir(file)
}

func (host isolatedHost) installIntegration(t *testing.T) {
	t.Helper()
	host.runAHT(t, "manage", "integrations", "install", string(host.contract.ID), "--binary", host.aht)
}

func (host isolatedHost) assertIntegrationCurrent(t *testing.T) {
	t.Helper()
	output := host.runAHT(t, "--json", "manage", "integrations", "status", string(host.contract.ID))
	var statuses []struct {
		Status string   `json:"status"`
		Paths  []string `json:"paths"`
	}
	if err := json.Unmarshal(output, &statuses); err != nil {
		t.Fatalf("decoding integration status: %v\n%s", err, output)
	}
	if len(statuses) != 1 || statuses[0].Status != "current" {
		t.Fatalf("installed integration is not current:\n%s", output)
	}
	for _, path := range statuses[0].Paths {
		found := false
		_ = filepath.Walk(path, func(candidate string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return nil
			}
			content, readErr := os.ReadFile(candidate)
			if readErr == nil && bytes.Contains(content, []byte(host.aht)) {
				found = true
			}
			return nil
		})
		if found {
			return
		}
	}
	t.Fatalf("current integration does not reference compatibility oracle %s:\n%s", host.aht, output)
}

func (host isolatedHost) assertVersion(t *testing.T) {
	t.Helper()
	command := exec.Command(host.hostPath, host.contract.VersionArgs...)
	command.Env = host.env
	command.Dir = host.work
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s version probe failed: %v\n%s\n%s", host.contract.ID, err, output, stderr.Bytes())
	}
	if len(bytes.TrimSpace(output)) == 0 {
		t.Fatalf("%s version probe returned no version", host.contract.ID)
	}
	t.Logf("current %s: %s", host.contract.ID, bytes.TrimSpace(output))
}

func (host *isolatedHost) runLifecycle(t *testing.T) {
	t.Helper()

	toolName := lifecycleToolName(host.contract.ID)
	host.provider = newScriptedProvider(t, host.contract.Protocol, toolName, lifecycleToolArgs(host.contract.ID), "aht-compat-marker")
	if host.contract.ID == registry.HarnessOpenClaw {
		host.provider.callID = "callcompat" // OpenClaw strips punctuation from tool IDs.
	}
	host.startTracker(t)
	command, setup := host.lifecycleCommand(t)
	for _, setupCommand := range setup {
		output, err := setupCommand.CombinedOutput()
		if err != nil {
			t.Fatalf("%s provider setup failed: %v\n%s", host.contract.ID, err, output)
		}
	}
	hostOutput := host.runHostCommand(t, command)
	if providerErr := host.provider.Error(); providerErr != nil {
		t.Fatalf("%v\nprovider requests: %s", providerErr, providerRequestSummary(host.provider))
	}
	if len(host.provider.Requests()) != 2 {
		t.Fatalf("%s provider request count = %d, want exactly 2\n%s", host.contract.ID, len(host.provider.Requests()), hostOutput)
	}

	host.waitForSession(t, hostOutput)
}

func (host isolatedHost) validateSession(session registry.Session) bool {
	if host.contract.Level == compatibilityDiscovery {
		return true
	}
	if session.Observations.Native == nil {
		return false
	}
	if session.Observations.Native.SessionID == "" {
		return false
	}
	if session.SessionID != "" && session.SessionID != session.Observations.Native.SessionID {
		return false
	}
	if host.work != "" && filepath.Clean(session.CWD) != filepath.Clean(host.work) {
		return false
	}
	return true
}

func (host isolatedHost) waitForSession(t *testing.T, hostOutput []byte) {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		listOutput := host.runAHT(t, "--json", "list", "--agent", string(host.contract.ID))
		var sessions []registry.Session
		if err := json.Unmarshal(listOutput, &sessions); err != nil {
			t.Fatalf("decoding final session list: %v\n%s", err, listOutput)
		}
		for _, s := range sessions {
			if host.validateSession(s) {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("%s completed provider lifecycle without matching native session evidence\nsessions: %s\n%s", host.contract.ID, listOutput, hostOutput)
		case <-ticker.C:
		}
	}
}

func TestSessionOracleRequiresNativeObservation(t *testing.T) {
	t.Parallel()

	workDir := "/tmp/project"
	processOnly := registry.Session{
		ID:       "s1",
		Harness:  registry.HarnessOpenCode,
		Presence: registry.PresenceLive,
		CWD:      workDir,
		Observations: registry.Observations{
			Process: &registry.ProcessObservation{
				Present: true,
			},
		},
	}
	host := isolatedHost{work: workDir, contract: hostContract{ID: registry.HarnessOpenCode, Level: compatibilityLifecycle}}
	if host.validateSession(processOnly) {
		t.Fatal("oracle accepted process-only session for non-exempt harness")
	}

	withNative := processOnly
	withNative.SessionID = "opencode-1"
	withNative.Observations.Native = &registry.NativeObservation{
		SessionID: "opencode-1",
		Event:     "agent_start",
	}
	if !host.validateSession(withNative) {
		t.Fatal("oracle rejected valid native session")
	}

	wrongCWD := withNative
	wrongCWD.CWD = "/tmp/other"
	if host.validateSession(wrongCWD) {
		t.Fatal("oracle accepted session with mismatched CWD")
	}
	mismatchedID := withNative
	mismatchedID.SessionID = "different-session"
	if host.validateSession(mismatchedID) {
		t.Fatal("oracle accepted session with mismatched native session ID")
	}

	cursorHost := isolatedHost{work: workDir, contract: hostContract{ID: registry.HarnessCursor, Level: compatibilityDiscovery}}
	if !cursorHost.validateSession(processOnly) {
		t.Fatal("oracle rejected discovery-only state for exempt cursor harness")
	}
}

func (host isolatedHost) startTracker(t *testing.T) {
	t.Helper()

	logFile, err := os.Create(filepath.Join(host.root, "tracker.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(host.aht, "--store", host.store, "manage", "tracker", "run", "--quiet")
	command.Env = host.env
	command.Dir = host.work
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = logFile.Close()
	})

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	socketPath := broker.SocketPath(host.store)
	for {
		select {
		case <-deadline.C:
			t.Fatalf("tracker did not create broker socket %s", socketPath)
		case <-ticker.C:
			info, statErr := os.Stat(socketPath)
			if statErr == nil && info.Mode()&os.ModeSocket != 0 {
				return
			}
		}
	}
}

func (host isolatedHost) runHostCommand(t *testing.T, command *exec.Cmd) []byte {
	t.Helper()

	logPath := filepath.Join(host.root, fmt.Sprintf("%s-host.log", host.contract.ID))
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() {
		wait <- command.Wait()
	}()
	var runErr error
	select {
	case runErr = <-wait:
	case <-time.After(30 * time.Second):
		_ = command.Process.Kill()
		runErr = <-wait
		_ = logFile.Close()
		output, _ := os.ReadFile(logPath)
		t.Fatalf("%s lifecycle timed out after 30s: %v\nprovider requests: %s\n%s", host.contract.ID, runErr, providerRequestSummary(host.provider), output)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	output, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if host.contract.ID == registry.HarnessKimiCode {
		kimiLog, _ := os.ReadFile(filepath.Join(host.root, "kimi", "logs", "kimi.log"))
		output = append(output, kimiLog...)
	}
	if runErr != nil {
		t.Fatalf("%s lifecycle command failed: %v\nprovider requests: %s\n%s", host.contract.ID, runErr, providerRequestSummary(host.provider), output)
	}
	return output
}

func providerRequestSummary(provider *scriptedProvider) string {
	if provider == nil {
		return "none"
	}
	requests := provider.Requests()
	summary := make([]string, 0, len(requests))
	for _, request := range requests {
		body := request.Body
		var payload map[string]any
		if json.Unmarshal([]byte(body), &payload) == nil {
			if tools, exists := payload["tools"].([]any); exists {
				names := make([]string, 0, len(tools))
				keys := make([]string, 0, len(payload))
				for key := range payload {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, rawTool := range tools {
					tool, ok := rawTool.(map[string]any)
					if !ok {
						continue
					}
					if name, ok := tool["name"].(string); ok {
						names = append(names, name)
					}
					if function, ok := tool["function"].(map[string]any); ok {
						if name, ok := function["name"].(string); ok {
							names = append(names, name)
						}
					}
				}
				input := payload["input"]
				if input == nil {
					input = payload["messages"]
				}
				body = "input_tail=" + summarizeProviderInput(input) + " keys=" + strings.Join(keys, ",") + " tools=" + strings.Join(names, ",")
			}
		}
		if len(body) > 2048 {
			body = body[:2048] + "..."
		}
		summary = append(summary, fmt.Sprintf("%s %s %s", request.Method, request.Path, body))
	}
	return strings.Join(summary, "\n")
}

func summarizeProviderInput(value any) string {
	items, ok := value.([]any)
	if !ok {
		return fmt.Sprint(value)
	}
	relevant := make([]any, 0, len(items))
	for _, item := range items {
		message, messageOK := item.(map[string]any)
		if !messageOK {
			continue
		}
		role, _ := message["role"].(string)
		messageType, _ := message["type"].(string)
		if role == "user" || role == "assistant" || role == "tool" || strings.Contains(messageType, "function_call") {
			relevant = append(relevant, message)
		}
	}
	if len(relevant) == 0 {
		relevant = items
	}
	if len(relevant) > 3 {
		relevant = relevant[len(relevant)-3:]
	}
	summary := mustJSON(relevant)
	if len(summary) > 2000 {
		return "..." + summary[len(summary)-2000:]
	}
	return summary
}

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
	if id == registry.HarnessGrok {
		return map[string]any{"command": "printf aht-compat-marker", "description": "Print the compatibility marker"}
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
		args = []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "-C", host.work, compatibilityPrompt}
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
		args = []string{"-p", "--provider", "aht-compat", "--model", "compat", "--approve", compatibilityPrompt}
	case registry.HarnessKimiCode:
		host.configureKimiModel(t, baseURL)
		args = []string{"--print", "--final-message-only", "--no-thinking", "--model", "aht-compat", "--max-steps-per-turn", "2", "-p", compatibilityPrompt}
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
		host.writeFile(t, filepath.Join(configDir, "opencode.json"), openCodeConfigJSON(baseURL))
		args = []string{"run", "--model", "aht-compat/compat", "--auto", "--dir", host.work, compatibilityPrompt}
	case registry.HarnessHermes:
		config := fmt.Sprintf("model:\n  default: compat\n  provider: custom\n  base_url: %q\n  api_key: compat\n  api_mode: chat_completions\nplugins:\n  enabled:\n    - aht-state\n", baseURL)
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
		args = []string{"exec", "--model", "custom:aht-compat-0", "--skip-permissions-unsafe", "--cwd", host.work, compatibilityPrompt}
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

func (host isolatedHost) writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (host isolatedHost) runAHT(t *testing.T, args ...string) []byte {
	t.Helper()
	command := exec.Command(host.aht, append([]string{"--store", host.store}, args...)...)
	command.Env = host.env
	command.Dir = host.work
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("aht %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func (host isolatedHost) configureKimiModel(t *testing.T, baseURL string) {
	t.Helper()

	path := filepath.Join(host.root, "kimi", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("default_model = \"aht-compat\"\ntelemetry = false\n\n[providers.aht-compat]\ntype = \"openai_responses\"\nbase_url = %q\napi_key = \"compat\"\n\n[models.aht-compat]\nprovider = \"aht-compat\"\nmodel = \"compat\"\nmax_context_size = 128000\n\n%s", baseURL, string(data))
	host.writeFile(t, path, config)
}

func (host isolatedHost) configureDroidModel(t *testing.T, baseURL string) {
	t.Helper()

	path := filepath.Join(host.home, ".factory", "settings.json")
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
	}
	settings["customModels"] = []any{map[string]any{
		"model":           "compat",
		"displayName":     "aht-compat",
		"baseUrl":         baseURL,
		"apiKey":          "compat",
		"provider":        "generic-chat-completion-api",
		"maxOutputTokens": 4096,
	}}
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	host.writeFile(t, path, string(updated))
}

func (host isolatedHost) configureOpenClawModel(t *testing.T, baseURL string) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(host.home, ".openclaw", "openclaw.json")
	settings := make(map[string]any)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
	}
	defaults := mapValue(mapValue(settings, "agents"), "defaults")
	defaults["workspace"] = host.work
	defaults["model"] = map[string]any{"primary": "aht-compat/compat"}
	providers := mapValue(mapValue(settings, "models"), "providers")
	providers["aht-compat"] = map[string]any{
		"baseUrl": baseURL,
		"apiKey":  "compat",
		"api":     "openai-completions",
		"models": []any{map[string]any{
			"id": "compat", "name": "AHT compatibility", "reasoning": false,
			"input": []string{"text"}, "contextWindow": 32000, "maxTokens": 4096,
		}},
	}
	plugins := mapValue(settings, "plugins")
	plugins["allow"] = []string{"aht-state"}
	entries := mapValue(plugins, "entries")
	entries["fireworks"] = map[string]any{"enabled": false}
	entries["perplexity"] = map[string]any{"enabled": false}
	settings["gateway"] = map[string]any{
		"mode": "local",
		"port": port,
		"auth": map[string]any{"mode": "none"},
	}
	updated, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	host.writeFile(t, path, string(updated))
	return port
}

func (host isolatedHost) startOpenClawGateway(t *testing.T, port int) {
	t.Helper()

	logFile, err := os.Create(filepath.Join(host.root, "openclaw-gateway.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(host.hostPath, "gateway", "run", "--bind", "loopback", "--port", fmt.Sprint(port), "--auth", "none")
	command.Env = host.env
	command.Dir = host.work
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = logFile.Close()
	})
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	output, _ := os.ReadFile(filepath.Join(host.root, "openclaw-gateway.log"))
	t.Fatalf("OpenClaw gateway did not listen on %s\n%s", address, output)
}

func mapValue(parent map[string]any, key string) map[string]any {
	value, ok := parent[key].(map[string]any)
	if ok {
		return value
	}
	value = make(map[string]any)
	parent[key] = value
	return value
}

func piModelsJSON(baseURL string) string {
	return fmt.Sprintf(`{"providers":{"aht-compat":{"baseUrl":%q,"api":"openai-completions","apiKey":"compat","compat":{"supportsDeveloperRole":false,"supportsReasoningEffort":false},"models":[{"id":"compat","name":"AHT compatibility","reasoning":false,"input":["text"],"contextWindow":32000,"maxTokens":4096}]}}}`, baseURL)
}

func ompModelsYAML(baseURL string) string {
	return fmt.Sprintf("providers:\n  aht-compat:\n    baseUrl: %q\n    api: openai-completions\n    apiKey: compat\n    models:\n      - id: compat\n        name: AHT compatibility\n        reasoning: false\n        input: [text]\n        contextWindow: 32000\n        maxTokens: 4096\n", baseURL)
}

func openCodeConfigJSON(baseURL string) string {
	return fmt.Sprintf(`{"$schema":"https://opencode.ai/config.json","provider":{"aht-compat":{"npm":"@ai-sdk/openai-compatible","name":"AHT compatibility","options":{"baseURL":%q,"apiKey":"compat"},"models":{"compat":{"name":"AHT compatibility"}}}}}`, baseURL)
}

func TestCompatibilityTimeoutBudget(t *testing.T) {
	if deadline, ok := t.Deadline(); ok && time.Until(deadline) < 45*time.Second {
		t.Fatalf("compatibility test requires at least 45 seconds, remaining %s", time.Until(deadline))
	}
}
