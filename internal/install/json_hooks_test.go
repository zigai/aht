package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"

	"github.com/zigai/aht/v2/internal/harness"
)

// requireNoSubagentHooks asserts that child-agent events, which fire while the
// parent turn keeps running, never report the main session's activity.
func requireNoSubagentHooks(t *testing.T, hooks map[string]any, events ...string) {
	t.Helper()

	for _, event := range events {
		if _, ok := hooks[event]; ok {
			t.Fatalf("expected no %s hook: subagent events do not describe the main turn", event)
		}
	}
}

// requireCompactionKeepsTurnRunning asserts that SessionStart reports idle only
// for real session starts, while compaction-sourced SessionStart, which also
// fires after automatic compaction inside a running turn, claims presence alone.
func requireCompactionKeepsTurnRunning(t *testing.T, hooks map[string]any, idleMatcher string) {
	t.Helper()

	commands := requireTestHookMatcherCommands(t, hooks, hookEventSessionStart)
	for matcher, command := range commands {
		if !strings.Contains(command, " report ") {
			delete(commands, matcher)
		}
	}

	if len(commands) != 2 {
		t.Fatalf("expected idle and compact SessionStart hooks, got %#v", commands)
	}

	if command := commands[idleMatcher]; !strings.Contains(
		command,
		"--activity idle --event SessionStart",
	) {
		t.Fatalf("SessionStart %q hook = %q, want idle report", idleMatcher, command)
	}

	compact := commands["compact"]
	if !strings.Contains(compact, "--presence live --event SessionStart") ||
		strings.Contains(compact, "--activity") {
		t.Fatalf("SessionStart compact hook = %q, want presence-only report", compact)
	}
}

// requireOnlyManualPreCompactRunning asserts that automatic compaction reports no
// activity, because it can run between turns where no Stop returns the session to idle.
func requireOnlyManualPreCompactRunning(t *testing.T, hooks map[string]any) {
	t.Helper()

	commands := requireTestHookMatcherCommands(t, hooks, "PreCompact")
	if len(commands) != 1 ||
		!strings.Contains(commands["manual"], "--activity running --event PreCompact") {
		t.Fatalf("PreCompact hooks = %#v, want one running report matching manual", commands)
	}
}

// requireManualPostCompactIdle asserts that only manual compaction returns the
// session to idle; automatic compaction continues the running turn.
func requireManualPostCompactIdle(t *testing.T, hooks map[string]any, manualMatcher string) {
	t.Helper()

	commands := requireTestHookMatcherCommands(t, hooks, "PostCompact")
	if len(commands) != 1 ||
		!strings.Contains(commands[manualMatcher], "--activity idle --event PostCompact") {
		t.Fatalf(
			"PostCompact hooks = %#v, want one idle report matching %q",
			commands,
			manualMatcher,
		)
	}
}

func requireTestHookMatcherCommands(
	t *testing.T,
	hooks map[string]any,
	event string,
) map[string]string {
	t.Helper()

	groups, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("expected %s hook groups, got %#v", event, hooks[event])
	}

	commands := make(map[string]string, len(groups))
	for _, value := range groups {
		group, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected %s hook group object, got %#v", event, value)
		}

		matcher, _ := group["matcher"].(string)

		handlers, ok := group["hooks"].([]any)
		if !ok || len(handlers) != 1 {
			t.Fatalf("expected one %s hook handler, got %#v", event, group["hooks"])
		}

		handler, ok := handlers[0].(map[string]any)
		if !ok {
			t.Fatalf("expected %s hook handler object, got %#v", event, handlers[0])
		}

		command, _ := handler["command"].(string)
		commands[matcher] = command
	}

	return commands
}

func TestInstallRemovesManagedHooksForDroppedEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QWEN_HOME", dir)
	path := filepath.Join(dir, "settings.json")
	userCommand := "notify-send subagent-finished"

	oldConfig := `{"hooks":{` +
		`"SubagentStart":[{"hooks":[{"type":"command","command":"aht ` +
		`report qwen --activity running --event SubagentStart ` +
		`--reporter-version 9 --reporter qwen-hook --raw-stdin-defaults-only ` +
		`--quiet"}]}],` +
		`"SubagentStop":[{"hooks":[` +
		`{"type":"command","command":"aht report qwen --activity ` +
		`idle --event SubagentStop --reporter-version 9 --reporter ` +
		`qwen-hook --raw-stdin-defaults-only --quiet"},` +
		`{"type":"command","command":"` + userCommand + `"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	options := Options{Harness: registry.Harness("qwen"), Binary: testInstallBinary}
	options.DryRun = true

	dryRun, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}

	if !dryRun.Changed {
		t.Fatal("expected stale managed subagent hooks to make the integration differ")
	}

	options.DryRun = false
	if _, err := Run(t.Context(), options); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data := readTestFile(t, path, "reading qwen hooks")
	config := decodeTestJSONObject(t, data, "qwen hooks")

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks object")
	}

	if _, ok := hooks["SubagentStart"]; ok {
		t.Fatalf("expected managed SubagentStart hook to be removed: %s", data)
	}

	text := string(data)
	if !strings.Contains(text, userCommand) {
		t.Fatalf("expected user SubagentStop hook to be preserved: %s", data)
	}

	if strings.Contains(text, "--event SubagentStop") {
		t.Fatalf("expected managed SubagentStop hook to be removed: %s", data)
	}

	second, err := Run(t.Context(), options)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if second.Changed {
		t.Fatal("expected second qwen install to be idempotent")
	}
}

func TestInstallRepairsManagedHookMatcher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QWEN_HOME", dir)

	first, err := Run(t.Context(), Options{
		Harness: registry.Harness("qwen"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("initial Run returned error: %v", err)
	}

	config := decodeTestJSONObject(
		t,
		readTestFile(t, first.Path, "reading qwen hooks"),
		"qwen hooks",
	)
	hooks := requireTestHooks(t, config)

	notificationGroups, ok := hooks["Notification"].([]any)
	if !ok || len(notificationGroups) == 0 {
		t.Fatalf("expected Notification hook groups, got %#v", hooks["Notification"])
	}

	group, ok := notificationGroups[0].(map[string]any)
	if !ok {
		t.Fatalf("expected Notification hook group object, got %#v", notificationGroups[0])
	}

	group["matcher"] = "*"

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("encoding modified hooks: %v", err)
	}

	if err := os.WriteFile(first.Path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("writing modified hooks: %v", err)
	}

	second, err := Run(t.Context(), Options{
		Harness: registry.Harness("qwen"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("repair Run returned error: %v", err)
	}

	if !second.Changed {
		t.Fatal("expected reinstall to repair stale managed matcher")
	}

	text := string(readTestFile(t, first.Path, "reading repaired hooks"))
	if !strings.Contains(text, `"matcher": "permission_prompt"`) ||
		strings.Contains(text, `"matcher": "*"`) {
		t.Fatalf("expected repaired notification matcher, got %s", text)
	}
}

func TestInstallCodexMergesHooks(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	installFakeCodexCLI(t)

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("codex"),
		Binary:  defaultBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected codex install to report changed")
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("installed hooks are not valid JSON: %v", err)
	}

	hooks, hooksOK := config["hooks"].(map[string]any)
	if !hooksOK {
		t.Fatal("expected hooks object")
	}

	_, hasSessionStart := hooks[hookEventSessionStart]
	if !hasSessionStart {
		t.Fatal("expected SessionStart hook")
	}

	_, hasUserPrompt := hooks["UserPromptSubmit"]
	if !hasUserPrompt {
		t.Fatal("expected UserPromptSubmit hook")
	}

	for _, event := range []string{"PostToolUse", "PreCompact", "PostCompact", "SessionEnd"} {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("expected %s hook", event)
		}
	}

	requireNoSubagentHooks(t, hooks, "SubagentStart", "SubagentStop")
	requireCompactionKeepsTurnRunning(t, hooks, "startup|resume|clear")
	requireManualPostCompactIdle(t, hooks, "manual")

	postToolCommand := requireTestHookCommand(t, hooks, "PostToolUse")
	if !strings.Contains(postToolCommand, "--raw-stdin-defaults-only") ||
		strings.Contains(postToolCommand, "--raw-stdin ") {
		t.Fatalf("Codex PostToolUse hook stores full tool output: %q", postToolCommand)
	}

	if timeoutSeconds := requireTestHookTimeoutSeconds(
		t,
		hooks,
		"SessionEnd",
	); timeoutSeconds != 3 {
		t.Fatalf("Codex SessionEnd hook timeout = %v, want 3", timeoutSeconds)
	}

	if !strings.Contains(string(data), "--presence gone --event SessionEnd") ||
		!strings.Contains(string(data), `"matcher": "other"`) {
		t.Fatalf("Codex SessionEnd hook is incomplete: %s", data)
	}
}

func TestInstallCodexReplacesManagedHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	installFakeCodexCLI(t)

	path := filepath.Join(dir, "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating codex dir: %v", err)
	}

	oldConfig := `{"hooks":{"SessionStart":[{"matcher":"startup|resume",` +
		`"hooks":[{"type":"command","command":"old-aht report ` +
		`--harness codex --state idle --source codex-hook"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	requireManagedReplacement(t, managedReplacementCase{
		Harness:              registry.Harness("codex"),
		Path:                 path,
		RemovedText:          "old-aht",
		RequiredText:         []string{"--raw-stdin", "--quiet"},
		FirstChangeMessage:   "expected codex install to replace old managed hook",
		SecondChangedMessage: "expected second codex install to be idempotent",
	})
}

func TestInstallCodexReplacesStaleHooksAndPreservesSymlinks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	installFakeCodexCLI(t)
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "hooks.json")

	oldConfig := `{"hooks":{"Stop":[{"hooks":[{"type":"command",` +
		`"command":"user-stop-hook","timeout":600}]},` +
		`{"hooks":[{"type":"command","command":"aht report codex ` +
		`--activity idle --event Stop --attribute ` +
		`aht_integration_version=4 --attribute ` +
		`aht_integration=codex-hook --queue --raw-stdin ` +
		`--quiet"}]}]}}`
	if err := os.WriteFile(targetPath, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing target hooks: %v", err)
	}

	symlinkPath := filepath.Join(dir, "hooks.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("codex"),
		Binary:  "/bin/aht-test",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected codex install to report changed")
	}

	fi, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected hooks.json to remain a symlink")
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading target file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "user-stop-hook") {
		t.Fatalf("expected user stop hook to be preserved: %s", content)
	}

	if strings.Contains(content, "aht_integration_version=4") {
		t.Fatalf("expected stale aht hook to be removed: %s", content)
	}

	if !strings.Contains(content, "/bin/aht-test report codex") ||
		!strings.Contains(content, "--reporter codex-hook") {
		t.Fatalf("expected new aht hook in target: %s", content)
	}
}

func requireCodexFixtureObject(t *testing.T, value any) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("fixture config value has type %T, want object", value)
	}

	return object
}

func TestCodexInstallTrustsOwnHooksAndRepairsUnchangedReinstall(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	statePath := installFakeCodexCLI(t)

	userHooks := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"user-start-command"}]}]}}`
	if err := writeCodexFixtureFile(
		filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json"),
		[]byte(userHooks),
	); err != nil {
		t.Fatal(err)
	}

	before := readCodexFixtureConfig(t, statePath)
	beforeState := requireCodexFixtureObject(t, requireTestHooks(t, before)["state"])
	disabledKey := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json") + ":session_start:1:0"
	beforeState[disabledKey] = map[string]any{"enabled": false, "trusted_hash": "sha256:previous"}

	writeCodexFixtureConfig(t, statePath, before)

	options := Options{Harness: registry.Harness("codex"), Binary: "/bin/fixture-aht"}

	installed, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	assertCodexFixtureTrust(t, statePath, before)
	hookBytes := readTestFile(t, installed.Path, "reading installed hooks")
	config := readCodexFixtureConfig(t, statePath)
	state := requireCodexFixtureObject(t, requireTestHooks(t, config)["state"])
	delete(requireCodexFixtureObject(t, state[disabledKey]), "trusted_hash")
	writeCodexFixtureConfig(t, statePath, config)
	unapproved := readTestFile(t, statePath, "reading unapproved config")

	status, err := Inspect(t.Context(), options.Harness, options.Binary)
	if err != nil || status.Status != ArtifactStale {
		t.Fatalf("missing approval status = %+v, %v", status, err)
	}

	if string(readTestFile(t, statePath, "reading config after inspection")) != string(unapproved) {
		t.Fatal("status inspection mutated native approval config")
	}

	reinstalled, err := Run(t.Context(), options)
	if err != nil || reinstalled.Changed {
		t.Fatalf("unchanged reinstall = %+v, %v", reinstalled, err)
	}

	if string(readTestFile(t, installed.Path, "reading unchanged hooks")) != string(hookBytes) {
		t.Fatal("approval repair changed hook definitions")
	}

	assertCodexFixtureTrust(t, statePath, before)

	status, err = Inspect(t.Context(), options.Harness, options.Binary)
	if err != nil || status.Status != ArtifactCurrent {
		t.Fatalf("repaired approval status = %+v, %v", status, err)
	}

	options.Binary = "/bin/updated-fixture-aht"
	if _, err := Run(t.Context(), options); err != nil {
		t.Fatal(err)
	}

	assertCodexFixtureTrust(t, statePath, before)
}

func assertCodexFixtureTrust(t *testing.T, path string, before map[string]any) {
	t.Helper()

	config, state, err := loadCodexFixtureConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	assertCodexFixturePreservedSettings(t, config, state, before)

	metadata, err := codexFixtureHooks(state)
	if err != nil {
		t.Fatal(err)
	}

	owned := 0

	for _, hook := range metadata {
		if hook["command"] == "user-start-command" {
			key, ok := hook["key"].(string)
			if !ok {
				t.Fatalf("fixture hook key has type %T, want string", hook["key"])
			}

			if _, exists := state[key]; exists {
				t.Fatal("installer approved an unrelated user command")
			}

			continue
		}

		owned++

		if hook["trustStatus"] != "trusted" {
			t.Fatalf("installed command has no current native approval: %#v", hook)
		}
	}

	if owned == 0 || len(state) != owned+1 {
		t.Fatalf("approved hooks = %d, config state = %#v", owned, state)
	}
}

func assertCodexFixturePreservedSettings(
	t *testing.T,
	config map[string]any,
	state map[string]any,
	before map[string]any,
) {
	t.Helper()

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("fixture hooks config is not an object")
	}

	beforeHooks, ok := before["hooks"].(map[string]any)
	if !ok {
		t.Fatal("initial fixture hooks config is not an object")
	}

	beforeState, ok := beforeHooks["state"].(map[string]any)
	if !ok {
		t.Fatal("initial fixture approval state is not an object")
	}

	if config["model"] != before["model"] || hooks["enabled"] != false ||
		!reflect.DeepEqual(state["unrelated-native-key"], beforeState["unrelated-native-key"]) {
		t.Fatalf("approval altered unrelated settings: %#v", config)
	}

	disabledKey := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json") + ":session_start:1:0"
	if approval, ok := state[disabledKey].(map[string]any); !ok || approval["enabled"] != false {
		t.Fatalf("approval changed the existing disabled flag: %#v", state[disabledKey])
	}
}

func TestCodexInstallPropagatesNativeTrustFailures(t *testing.T) {
	for _, failure := range []string{"rpc", "discovery", "hash", "missing command", "write", "verification"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("CODEX_HOME", t.TempDir())
			installFakeCodexCLI(t)
			t.Setenv("AHT_TEST_CODEX_FAILURE", failure)

			if _, err := Run(
				t.Context(),
				Options{Harness: registry.Harness("codex"), Binary: "/bin/fixture-aht"},
			); err == nil {
				t.Fatalf("installation succeeded despite native %s failure", failure)
			}
		})
	}
}

func TestCodexDryRunDoesNotRequireCLIOrMutateFiles(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	statePath := installFakeCodexCLI(t)
	before := readTestFile(t, statePath, "reading fixture config")
	t.Setenv("PATH", t.TempDir())

	options := Options{Harness: registry.Harness("codex"), Binary: "/bin/fixture-aht", DryRun: true}

	result, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
		t.Fatalf("dry run created hook definitions: %v", err)
	}

	if string(
		readTestFile(t, statePath, "reading fixture config after preview"),
	) != string(
		before,
	) {
		t.Fatal("dry run changed native approval config")
	}

	options.DryRun = false
	if _, err := Run(t.Context(), options); err == nil {
		t.Fatal("native install succeeded without Codex")
	}
}

func TestInstallRefusesHookConfigWithUnexpectedShape(t *testing.T) {
	for _, test := range []struct {
		name    string
		harness string
		config  string
	}{
		{name: "qwen event object", harness: "qwen", config: `{"hooks": {"Stop": {"command": "my-notifier"}}}`},
		{name: "qwen hooks string", harness: "qwen", config: `{"hooks": "my-notifier"}`},
		{
			name:    "cursor event object",
			harness: "cursor",
			config:  `{"version": 1, "hooks": {"stop": {"command": "my-notifier"}}}`,
		},
		{name: "cursor hooks string", harness: "cursor", config: `{"version": 1, "hooks": "my-notifier"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("QWEN_HOME", filepath.Join(home, ".qwen"))

			path := filepath.Join(home, ".cursor", "hooks.json")
			if test.harness == "qwen" {
				path = filepath.Join(home, ".qwen", "settings.json")
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(test.config), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Run(
				t.Context(),
				Options{Harness: registry.Harness(test.harness), Binary: testInstallBinary},
			)
			if !errors.Is(err, harness.ErrHookConfigShape) {
				t.Fatalf("install error = %v, want errHookConfigShape", err)
			}

			if got := readTestFile(t, path, "reading hook config"); string(got) != test.config {
				t.Fatalf("install changed the user's config to:\n%s", got)
			}
		})
	}
}

func TestInstallCursorWritesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("cursor"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected cursor install to report changed")
	}

	if result.Path != filepath.Join(home, ".cursor", "hooks.json") {
		t.Fatalf("unexpected path %q", result.Path)
	}

	data := readTestFile(t, result.Path, "reading installed hooks")

	config := decodeTestJSONObject(t, data, "installed hooks")
	if config["version"] != float64(1) {
		t.Fatalf("expected cursor hooks version 1, got %#v", config["version"])
	}

	hooks := requireTestHooks(t, config)
	requireTestHookEvents(t, hooks, []string{
		"sessionStart",
		"beforeSubmitPrompt",
		"stop",
		"sessionEnd",
	})

	text := string(data)
	requireTextContainsAll(t, text, []string{
		"--raw-stdin-defaults-only",
		"--reporter cursor-hook",
		"continue",
	}, "cursor hooks")

	if strings.Contains(text, "--raw-stdin ") {
		t.Fatalf("expected defaults-only cursor hook commands: %s", text)
	}
}

func TestInstallCursorReplacesManagedHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating cursor dir: %v", err)
	}

	oldConfig := `{"version":1,"hooks":{"sessionStart":[{"command":"./` +
		`user-hook.sh"},{"command":"old-aht report --harness cursor ` +
		`--state idle --source cursor-hook --attribute ` +
		`aht_integration=cursor-hook"}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("cursor"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected cursor install to replace old managed hook")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}

	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}

	if !strings.Contains(text, "./user-hook.sh") {
		t.Fatalf("expected user hook to be preserved: %s", text)
	}

	second, err := Run(t.Context(), Options{
		Harness: registry.Harness("cursor"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if second.Changed {
		t.Fatal("expected second cursor install to be idempotent")
	}
}

func TestInstallCopilotWritesHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COPILOT_HOME", dir)

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("copilot"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected copilot install to report changed")
	}

	if result.Path != filepath.Join(dir, "hooks", copilotHookFileName) {
		t.Fatalf("unexpected path %q", result.Path)
	}

	config := decodeTestJSONObject(
		t,
		readTestFile(t, result.Path, "reading copilot hooks"),
		"copilot hooks",
	)
	if config["version"] != float64(1) {
		t.Fatalf("expected Copilot hooks version 1, got %#v", config["version"])
	}

	hooks := requireTestHooks(t, config)
	requireTestHookEvents(t, hooks, []string{
		"sessionStart",
		"userPromptSubmitted",
		"preToolUse",
		"permissionRequest",
		"postToolUse",
		"postToolUseFailure",
		"agentStop",
		"sessionEnd",
	})
	text := string(readTestFile(t, result.Path, "reading copilot hooks text"))
	requireTextContainsAll(t, text, []string{
		"--raw-stdin-defaults-only",
		"--reporter copilot-hook",
		"copilot_hook_event=preToolUse",
		managedMarker,
		"|| true",
	}, "copilot hooks")
}

func TestInstallDroidWritesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hooksPath := filepath.Join(home, ".factory", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
		t.Fatal(err)
	}

	userCommand := "/opt/local/bin/user-droid-hook"

	initial := `{
  "description": "user hooks",
  "foreign_setting": {"enabled": true},
  "PreToolUse": [{"matcher": "Execute", "hooks": [{"type": "command", "command": "` + userCommand + `"}]}],
  "CustomEvent": [{"hooks": [{"type": "command", "command": "/opt/local/bin/custom"}]}]
}`
	if err := os.WriteFile(hooksPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected droid install to report changed")
	}

	if result.Path != hooksPath {
		t.Fatalf("unexpected path %q", result.Path)
	}

	data := readTestFile(t, result.Path, "reading droid hooks")

	config := decodeTestJSONObject(t, data, "droid hooks")
	if _, wrapped := config["hooks"]; wrapped {
		t.Fatal("standalone Droid hooks.json must contain events at the root")
	}

	hooks := config
	if config["description"] != "user hooks" || !strings.Contains(string(data), userCommand) {
		t.Fatalf("Droid install did not preserve foreign settings/hooks: %s", data)
	}

	requireTestHookEvents(t, hooks, []string{
		"CustomEvent",
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"Notification",
		hookEventStop,
		"PreCompact",
		"SessionEnd",
	})
	requireNoSubagentHooks(t, hooks, "SubagentStop")

	text := string(data)
	requireTextContainsAll(t, text, []string{
		"--raw-stdin-defaults-only",
		"--reporter droid-hook",
	}, "droid hooks")

	if strings.Contains(text, "statusMessage") {
		t.Fatalf("expected Droid hooks not to include unsupported statusMessage field: %s", text)
	}

	second, err := Run(t.Context(), Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if second.Changed {
		t.Fatal("expected second droid install to be idempotent")
	}
}

func TestInstallQwenWritesHooks(t *testing.T) {
	qwenHome := t.TempDir()
	t.Setenv("QWEN_HOME", qwenHome)
	settingsPath := filepath.Join(qwenHome, "settings.json")
	userCommand := "/opt/local/bin/user-qwen-hook"

	initial := `{
  "$version": 3,
  "security": {"auth": {"selectedType": "openai"}},
  "hooks": {
    "PreToolUse": [{"matcher": "^run_shell_command$", "hooks": [{"type": "command", "command": "` + userCommand + `"}]}]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	result := installQwen(t)
	if !result.Changed || result.Path != settingsPath {
		t.Fatalf("unexpected qwen install result: %#v", result)
	}

	data := readTestFile(t, settingsPath, "reading qwen settings")

	config := decodeTestJSONObject(t, data, "qwen settings")
	if config["$version"] == nil || config["security"] == nil ||
		!strings.Contains(string(data), userCommand) {
		t.Fatalf("Qwen install did not preserve foreign settings/hooks: %s", data)
	}

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("Qwen hooks must live under the settings hooks key: %s", data)
	}

	requireTestHookEvents(t, hooks, []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"Notification",
		"PreCompact",
		"PostCompact",
		hookEventStop,
		"StopFailure",
		"SessionEnd",
	})
	requireNoSubagentHooks(t, hooks, "SubagentStop")

	if _, ok := hooks["PermissionRequest"]; ok {
		t.Fatalf("Qwen PermissionRequest also fires for auto-denied background agents: %s", data)
	}

	text := string(data)
	requireTextContainsAll(t, text, []string{
		"--raw-stdin-defaults-only",
		"--reporter qwen-hook",
		`"matcher": "permission_prompt"`,
		`"matcher": "idle_prompt"`,
	}, "qwen hooks")

	if strings.Contains(text, "statusMessage") {
		t.Fatalf("Qwen shows statusMessage while a hook runs; managed hooks must omit it: %s", text)
	}

	if installQwen(t).Changed {
		t.Fatal("expected second qwen install to be idempotent")
	}
}

func TestInstallQwenExpandsHome(t *testing.T) {
	for _, override := range []string{"~", "~/config", `~\config`} {
		t.Run(override, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("QWEN_HOME", override)
			t.Chdir(t.TempDir())

			settingsPath := filepath.Join(home, "settings.json")
			if override != "~" {
				settingsPath = filepath.Join(home, "config", "settings.json")
			}

			result := installQwen(t)
			if result.Path != settingsPath {
				t.Fatalf("installed path = %q, want %q", result.Path, settingsPath)
			}

			data := readTestFile(t, settingsPath, "reading expanded Qwen settings")

			config := decodeTestJSONObject(t, data, "Qwen settings")
			if _, ok := config["hooks"].(map[string]any); !ok {
				t.Fatalf("expanded Qwen settings contain no hooks: %s", data)
			}
		})
	}
}

func installQwen(t *testing.T) Result {
	t.Helper()

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("qwen"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	return result
}

func TestInstallGrokWritesHooks(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("grok"),
		Binary:  defaultBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected grok install to report changed")
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("installed hooks are not valid JSON: %v", err)
	}

	hooks, hooksOK := config["hooks"].(map[string]any)
	if !hooksOK {
		t.Fatal("expected hooks object")
	}

	for _, event := range []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"PermissionDenied",
		"PreCompact",
		"PostCompact",
		hookEventStop,
		"StopCancelled",
		"StopFailure",
		"SessionEnd",
	} {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("expected %s hook", event)
		}
	}

	requireNoSubagentHooks(t, hooks, "SubagentStart", "SubagentStop")
	requireManualPostCompactIdle(t, hooks, "manual")

	command := requireTestHookCommand(t, hooks, "StopCancelled")
	if !strings.Contains(command, "--activity interrupted --event StopCancelled") {
		t.Fatalf("StopCancelled command = %q", command)
	}

	text := string(data)
	if !strings.Contains(text, "--raw-stdin") || !strings.Contains(text, "--quiet") {
		t.Fatalf("expected stdin-aware quiet grok hook: %s", text)
	}

	if !strings.Contains(text, "--reporter grok-hook") {
		t.Fatalf("expected managed grok hook marker: %s", text)
	}

	if !strings.Contains(text, managedMarker) {
		t.Fatalf("expected managed marker in grok hooks: %s", text)
	}
}

func TestInstallGrokReplacesManagedHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROK_HOME", dir)

	path := filepath.Join(dir, "hooks", grokHookFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating grok dir: %v", err)
	}

	oldConfig := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command",` +
		`"command":"old-aht report --harness grok --state idle ` +
		`--source grok-hook --attribute aht_integration=grok-hook",` +
		`"statusMessage":"aht managed integration"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("grok"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected grok install to replace old managed hook")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}

	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}

	second, err := Run(t.Context(), Options{
		Harness: registry.Harness("grok"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if second.Changed {
		t.Fatal("expected second grok install to be idempotent")
	}
}

func TestJSONHooksPreserveLargeNumbersAndTimeoutSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	hooksPath := filepath.Join(home, ".factory", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o700); err != nil {
		t.Fatal(err)
	}

	// 9007199254740993 is 2^53 + 1, which loses precision if decoded into float64 (becomes 9007199254740992)
	initial := `{
  "foreign_large_id": 9007199254740993,
  "nested": {
    "large_num": 9007199254740995
  }
}`
	if err := os.WriteFile(hooksPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal("expected install to report changed")
	}

	data := readTestFile(t, hooksPath, "reading installed hooks")
	assertLargeNumbersPreserved(t, data, "during install")

	// Replace the timeout in hooks.json with equivalent spelling: 5.0
	content := string(data)

	contentWithAltTimeout := strings.ReplaceAll(content, `"timeout": 5`, `"timeout": 5.0`)
	if err := os.WriteFile(hooksPath, []byte(contentWithAltTimeout), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second install should be idempotent (no change needed despite 5.0 vs float64(5))
	secondResult, err := Run(t.Context(), Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if secondResult.Changed {
		t.Fatal("expected second install to be idempotent with 5.0 timeout spelling")
	}

	removeResult, err := Remove(t.Context(), Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	if !removeResult.Changed {
		t.Fatal("expected remove to report changed")
	}

	cleanedData := readTestFile(t, hooksPath, "reading cleaned hooks")
	assertLargeNumbersPreserved(t, cleanedData, "during removal")
}

func assertLargeNumbersPreserved(t *testing.T, data []byte, phase string) {
	t.Helper()

	text := string(data)
	if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "9007199254740995") {
		t.Fatalf("large integers were corrupted %s: %s", phase, data)
	}
}

func TestInstallCodexReportsInterruptWithinNativeTimeout(t *testing.T) {
	installFakeCodexCLI(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	result, err := Run(
		t.Context(),
		Options{Harness: registry.Harness("codex"), Binary: defaultBinary},
	)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}

	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatal("missing hooks object")
	}

	command := requireTestHookCommand(t, hooks, "Interrupt")
	if !strings.Contains(command, "--activity interrupted --event Interrupt") ||
		!strings.Contains(command, "--quiet") {
		t.Fatalf("Interrupt command = %q", command)
	}

	if timeoutSeconds := requireTestHookTimeoutSeconds(t, hooks, "Interrupt"); timeoutSeconds != 3 {
		t.Fatalf("Interrupt timeout = %v, want 3", timeoutSeconds)
	}
}

type managedReplacementCase struct {
	Harness              registry.Harness
	Path                 string
	RemovedText          string
	RequiredText         []string
	FirstChangeMessage   string
	SecondChangedMessage string
}

func requireManagedReplacement(t *testing.T, test managedReplacementCase) {
	t.Helper()

	result, err := Run(t.Context(), Options{
		Harness: test.Harness,
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !result.Changed {
		t.Fatal(test.FirstChangeMessage)
	}

	text := string(readTestFile(t, test.Path, "reading installed hooks"))
	if strings.Contains(text, test.RemovedText) {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}

	requireTextContainsAll(t, text, test.RequiredText, "installed hooks")

	second, err := Run(t.Context(), Options{
		Harness: test.Harness,
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	if second.Changed {
		t.Fatal(test.SecondChangedMessage)
	}

	if second.NextStep != "" {
		t.Fatalf("idempotent install unexpectedly requires activation: %q", second.NextStep)
	}
}

func requireTestHookCommand(t *testing.T, hooks map[string]any, event string) string {
	t.Helper()
	handler := requireTestHookHandler(t, hooks, event)

	command, ok := handler["command"].(string)
	if !ok || command == "" {
		t.Fatalf("expected %s hook command, got %#v", event, handler["command"])
	}

	return command
}

func requireTestHookTimeoutSeconds(t *testing.T, hooks map[string]any, event string) float64 {
	t.Helper()
	handler := requireTestHookHandler(t, hooks, event)

	timeoutSeconds, ok := handler["timeout"].(float64)
	if !ok {
		t.Fatalf("expected %s hook timeout, got %#v", event, handler["timeout"])
	}

	return timeoutSeconds
}

func requireTestHookHandler(t *testing.T, hooks map[string]any, event string) map[string]any {
	t.Helper()

	groups, ok := hooks[event].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected one %s hook group, got %#v", event, hooks[event])
	}

	group, ok := groups[0].(map[string]any)
	if !ok {
		t.Fatalf("expected %s hook group object, got %#v", event, groups[0])
	}

	handlers, ok := group["hooks"].([]any)
	if !ok || len(handlers) != 1 {
		t.Fatalf("expected one %s hook handler, got %#v", event, group["hooks"])
	}

	handler, ok := handlers[0].(map[string]any)
	if !ok {
		t.Fatalf("expected %s hook handler object, got %#v", event, handlers[0])
	}

	return handler
}

func TestJSONHookInstallAndRemovePreserveUserHookMentioningManagedFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := filepath.Join(home, ".factory", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	group := commandHookGroup(
		"",
		"echo report --reporter=droid-hook",
		"echo report droid --reporter=droid-hook --quiet",
	)
	want := map[string]any{"Stop": []any{group}}

	initial, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	opts := Options{Harness: registry.Harness("droid"), Binary: testInstallBinary}
	if _, err := Run(t.Context(), opts); err != nil {
		t.Fatalf("install droid hooks: %v", err)
	}

	installed := decodeTestJSONObject(
		t,
		readTestFile(t, path, "reading droid hooks after install"),
		"droid hooks",
	)

	groups, ok := installed["Stop"].([]any)
	if !ok || len(groups) != 2 || !reflect.DeepEqual(groups[0], group) {
		t.Fatalf("install lost or modified the user Stop hooks: %#v", installed["Stop"])
	}

	if _, err := Remove(t.Context(), opts); err != nil {
		t.Fatalf("remove droid hooks: %v", err)
	}

	cleaned := decodeTestJSONObject(
		t,
		readTestFile(t, path, "reading droid hooks after remove"),
		"droid hooks",
	)
	if !reflect.DeepEqual(cleaned, want) {
		t.Fatalf("remaining droid hooks = %#v, want only original user hooks %#v", cleaned, want)
	}
}
