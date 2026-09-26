package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	codexpkg "github.com/zigai/aht/v2/internal/harness/codex"
)

func TestInstallClaudeWritesHooks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.Harness("claude"),
		Binary:       defaultBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected claude install to report changed")
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("installed hooks are not valid JSON: %v", err)
	}
	requireClaudeHookEvents(t, config)

	requireTextContainsAll(t, string(data), []string{
		"--raw-stdin",
		"--quiet",
		"--reporter claude-hook",
		managedMarker,
	}, "installed hook")
}

func requireClaudeHookEvents(t *testing.T, config map[string]any) {
	t.Helper()
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
		"PermissionRequest",
		"PermissionDenied",
		"Notification",
		"PreCompact",
		"PostCompact",
		hookEventStop,
		"StopFailure",
		"SessionEnd",
	} {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("expected %s hook", event)
		}
	}
	for _, event := range []string{"SubagentStart", "SubagentStop"} {
		if _, ok := hooks[event]; ok {
			t.Fatalf("expected no %s hook: subagent events do not describe the main turn", event)
		}
	}
}

func TestInstallClaudeRemovesManagedHooksForDroppedEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	userCommand := "notify-send subagent-finished"
	oldConfig := `{"hooks":{` +
		`"SubagentStart":[{"hooks":[{"type":"command","command":"aht report claude --activity running --event SubagentStart --reporter-version 9 --reporter claude-hook --raw-stdin --quiet"}]}],` +
		`"SubagentStop":[{"hooks":[` +
		`{"type":"command","command":"aht report claude --activity idle --event SubagentStop --reporter-version 9 --reporter claude-hook --raw-stdin --quiet"},` +
		`{"type":"command","command":"` + userCommand + `"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	options := Options{Harness: registry.Harness("claude"), Binary: testInstallBinary}
	options.DryRun = true
	dryRun, err := Run(options)
	if err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	if !dryRun.Changed {
		t.Fatal("expected stale managed subagent hooks to make the integration differ")
	}
	options.DryRun = false
	if _, err := Run(options); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	data := readTestFile(t, path, "reading claude hooks")
	config := decodeTestJSONObject(t, data, "claude hooks")
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

	second, err := Run(options)
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second claude install to be idempotent")
	}
}

func TestInstallClaudeReplacesManagedHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating claude dir: %v", err)
	}
	oldConfig := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"old-aht report --harness claude --state idle --source claude-hook --attribute aht_integration_version=5 --attribute aht_integration=claude-hook","statusMessage":"aht managed integration"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	requireManagedReplacement(t, managedReplacementCase{
		Harness:              registry.Harness("claude"),
		Path:                 path,
		RemovedText:          "old-aht",
		RequiredText:         []string{"--raw-stdin"},
		FirstChangeMessage:   "expected claude install to replace old managed hook",
		SecondChangedMessage: "expected second claude install to be idempotent",
	})
}

func TestInstallClaudeRepairsManagedHookMatcher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	first, err := Run(Options{
		Harness:      registry.Harness("claude"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("initial Run returned error: %v", err)
	}

	config := decodeTestJSONObject(t, readTestFile(t, first.Path, "reading claude hooks"), "claude hooks")
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

	second, err := Run(Options{
		Harness:      registry.Harness("claude"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("repair Run returned error: %v", err)
	}
	if !second.Changed {
		t.Fatal("expected reinstall to repair stale managed matcher")
	}

	text := string(readTestFile(t, first.Path, "reading repaired hooks"))
	if !strings.Contains(text, `"matcher": "permission_prompt"`) || strings.Contains(text, `"matcher": "*"`) {
		t.Fatalf("expected repaired notification matcher, got %s", text)
	}
}

//nolint:cyclop // one install assertion verifies every required Codex hook shape
func TestInstallCodexMergesHooks(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.Harness("codex"),
		Binary:       defaultBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected codex install to report changed")
	}
	if result.NextStep != codexpkg.HookTrustNextStep {
		t.Fatalf("Codex install next step = %q", result.NextStep)
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
	for _, event := range []string{"PostToolUse", "PreCompact", "PostCompact", "SubagentStart", "SubagentStop", harnesspkg.HookEventSessionEnd} {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("expected %s hook", event)
		}
	}
	postToolCommand := requireTestHookCommand(t, hooks, "PostToolUse")
	if !strings.Contains(postToolCommand, "--raw-stdin-defaults-only") || strings.Contains(postToolCommand, "--raw-stdin ") {
		t.Fatalf("Codex PostToolUse hook stores full tool output: %q", postToolCommand)
	}
	if timeout := requireTestHookTimeout(t, hooks, harnesspkg.HookEventSessionEnd); timeout != 3 {
		t.Fatalf("Codex SessionEnd hook timeout = %v, want 3", timeout)
	}
	if !strings.Contains(string(data), "--presence gone --event SessionEnd") || !strings.Contains(string(data), `"matcher": "other"`) {
		t.Fatalf("Codex SessionEnd hook is incomplete: %s", data)
	}
}

func TestInstallCodexReplacesManagedHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	path := filepath.Join(dir, "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating codex dir: %v", err)
	}
	oldConfig := `{"hooks":{"SessionStart":[{"matcher":"startup|resume","hooks":[{"type":"command","command":"old-aht report --harness codex --state idle --source codex-hook"}]}]}}`
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
		ExpectedNextStep:     codexpkg.HookTrustNextStep,
	})
}

func TestInstallCodexReplacesStaleHooksAndPreservesSymlinks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "hooks.json")
	oldConfig := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"plannotator","timeout":345600}]},{"hooks":[{"type":"command","command":"aht report codex --activity idle --event Stop --attribute aht_integration_version=4 --attribute aht_integration=codex-hook --queue --raw-stdin --quiet"}]}]}}`
	if err := os.WriteFile(targetPath, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing target hooks: %v", err)
	}
	symlinkPath := filepath.Join(dir, "hooks.json")
	if err := os.Symlink(targetPath, symlinkPath); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	result, err := Run(Options{
		Harness: registry.Harness("codex"),
		Binary:  "/bin/aht-test",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected codex install to report changed")
	}

	// Verify symlink was preserved
	fi, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected hooks.json to remain a symlink")
	}

	// Verify content in target file
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading target file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "plannotator") {
		t.Fatalf("expected user plannotator hook to be preserved: %s", content)
	}
	if strings.Contains(content, "aht_integration_version=4") {
		t.Fatalf("expected stale aht hook to be removed: %s", content)
	}
	if !strings.Contains(content, "/bin/aht-test report codex") || !strings.Contains(content, "--reporter codex-hook") {
		t.Fatalf("expected new aht hook in target: %s", content)
	}
}

func TestInstallCursorWritesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(Options{
		Harness:      registry.Harness("cursor"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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
	oldConfig := `{"version":1,"hooks":{"sessionStart":[{"command":"./user-hook.sh"},{"command":"old-aht report --harness cursor --state idle --source cursor-hook --attribute aht_integration=cursor-hook"}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("cursor"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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

	second, err := Run(Options{
		Harness:      registry.Harness("cursor"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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

	result, err := Run(Options{
		Harness:      registry.Harness("copilot"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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

	config := decodeTestJSONObject(t, readTestFile(t, result.Path, "reading copilot hooks"), "copilot hooks")
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

	result, err := Run(Options{
		Harness:      registry.Harness("droid"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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
		"SubagentStop",
		"PreCompact",
		"SessionEnd",
	})
	text := string(data)
	requireTextContainsAll(t, text, []string{
		"--raw-stdin-defaults-only",
		"--reporter droid-hook",
	}, "droid hooks")
	if strings.Contains(text, "statusMessage") {
		t.Fatalf("expected Droid hooks not to include unsupported statusMessage field: %s", text)
	}

	second, err := Run(Options{
		Harness:      registry.Harness("droid"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second droid install to be idempotent")
	}
}

func TestInstallGrokWritesHooks(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.Harness("grok"),
		Binary:       defaultBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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
		"SubagentStart",
		"SubagentStop",
		"PreCompact",
		"PostCompact",
		hookEventStop,
		"StopFailure",
		"SessionEnd",
	} {
		if _, ok := hooks[event]; !ok {
			t.Fatalf("expected %s hook", event)
		}
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
	oldConfig := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"old-aht report --harness grok --state idle --source grok-hook --attribute aht_integration=grok-hook","statusMessage":"aht managed integration"}]}]}}`
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old hooks: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("grok"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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

	second, err := Run(Options{
		Harness:      registry.Harness("grok"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
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

	// Install Droid hooks
	result, err := Run(Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected install to report changed")
	}

	// Verify large numbers are preserved verbatim
	data := readTestFile(t, hooksPath, "reading installed hooks")
	assertLargeNumbersPreserved(t, data, "during install")

	// Replace the timeout in hooks.json with equivalent spelling: 5.0
	content := string(data)
	contentWithAltTimeout := strings.ReplaceAll(content, `"timeout": 5`, `"timeout": 5.0`)
	if err := os.WriteFile(hooksPath, []byte(contentWithAltTimeout), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second install should be idempotent (no change needed despite 5.0 vs float64(5))
	secondResult, err := Run(Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if secondResult.Changed {
		t.Fatal("expected second install to be idempotent with 5.0 timeout spelling")
	}

	// Remove hooks
	removeResult, err := Remove(Options{
		Harness: registry.Harness("droid"),
		Binary:  testInstallBinary,
	})
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if !removeResult.Changed {
		t.Fatal("expected remove to report changed")
	}

	// Verify foreign large numbers are still preserved after removal
	cleanedData := readTestFile(t, hooksPath, "reading cleaned hooks")
	assertLargeNumbersPreserved(t, cleanedData, "during removal")
}

func assertLargeNumbersPreserved(t *testing.T, data []byte, context string) {
	t.Helper()
	text := string(data)
	if !strings.Contains(text, "9007199254740993") || !strings.Contains(text, "9007199254740995") {
		t.Fatalf("large integers were corrupted %s: %s", context, data)
	}
}
