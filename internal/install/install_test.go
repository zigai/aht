package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	codexpkg "github.com/zigai/aht/internal/harness/codex"
	"github.com/zigai/aht/pkg/registry"
)

const (
	testInstallBinary     = "/usr/local/bin/aht"
	piExtensionName       = "aht-state.ts"
	ompExtensionName      = "aht-state.ts"
	openCodePluginName    = "aht-state.ts"
	kiloPluginName        = "aht-state.ts"
	agyPluginName         = "aht-state"
	agyMarkerFileName     = ".aht-managed"
	agyImportManifestName = "import_manifest.json"
	agyImportSource       = "antigravity"
	agyImportComponent    = "hooks"
	copilotHookFileName   = "aht.json"
	goosePluginName       = "aht-state"
	gooseMarkerFileName   = ".aht-managed"
	kimiCodeManagedStart  = "# BEGIN aht managed integration: kimi-code"
	kimiCodeManagedEnd    = "# END aht managed integration: kimi-code"
	grokHookFileName      = "aht-state.json"
	hookEventSessionStart = harnesspkg.HookEventSessionStart
	hookEventStop         = harnesspkg.HookEventStop
)

func TestContextAwareIntegrationEntryPointsPreserveCancellation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := Options{Harness: registry.HarnessCodex, Binary: testInstallBinary}

	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "install", run: func() error { _, err := RunContext(ctx, options); return err }},
		{name: "remove", run: func() error { _, err := RemoveContext(ctx, options); return err }},
		{name: "inspect", run: func() error { _, err := InspectContext(ctx, options.Harness, options.Binary); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("context-aware %s error = %v, want context.Canceled", test.name, err)
			}
		})
	}
}

//nolint:cyclop // one install assertion verifies every required Codex hook shape
func TestInstallCodexMergesHooks(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.HarnessCodex,
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
		Harness:              registry.HarnessCodex,
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
		Harness: registry.HarnessCodex,
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
	if !strings.Contains(content, "/bin/aht-test report codex") || !strings.Contains(content, "aht_integration=codex-hook") {
		t.Fatalf("expected new aht hook in target: %s", content)
	}
}

func TestInstallClaudeWritesHooks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.HarnessClaude,
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
		"aht_integration=claude-hook",
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
		Harness:              registry.HarnessClaude,
		Path:                 path,
		RemovedText:          "old-aht",
		RequiredText:         []string{"--raw-stdin", "aht_integration_version=7"},
		FirstChangeMessage:   "expected claude install to replace old managed hook",
		SecondChangedMessage: "expected second claude install to be idempotent",
	})
}

func TestInstallClaudeRepairsManagedHookMatcher(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	first, err := Run(Options{
		Harness:      registry.HarnessClaude,
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
		Harness:      registry.HarnessClaude,
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

func TestInstallCursorWritesHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(Options{
		Harness:      registry.HarnessCursor,
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
		"aht_integration=cursor-hook",
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
		Harness:      registry.HarnessCursor,
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
		Harness:      registry.HarnessCursor,
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
		Harness:      registry.HarnessCopilot,
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
		"aht_integration=copilot-hook",
		"copilot_hook_event=preToolUse",
		managedMarker,
		"|| true",
	}, "copilot hooks")
}

func TestInstallClineWritesNativePlugin(t *testing.T) {
	clineDir := filepath.Join(t.TempDir(), ".cline")
	t.Setenv("CLINE_DIR", clineDir)
	pluginDir := filepath.Join(clineDir, "plugins", "aht-state")

	result, err := Run(Options{
		Harness:      registry.HarnessCline,
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
		t.Fatal("expected cline install to report changed")
	}
	if result.Path != pluginDir {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireClinePackageManifest(t, pluginDir)
	requireClineAgentPlugin(t, pluginDir)
	requireClinePluginMarker(t, pluginDir)

	second, err := Run(Options{
		Harness:      registry.HarnessCline,
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
		t.Fatal("expected second cline install to be idempotent")
	}
}

func requireClinePackageManifest(t *testing.T, pluginDir string) {
	t.Helper()
	packageData := readTestFile(t, filepath.Join(pluginDir, "package.json"), "reading Cline plugin package")
	var packageManifest map[string]any
	if err := json.Unmarshal(packageData, &packageManifest); err != nil {
		t.Fatalf("parsing Cline plugin package: %v", err)
	}
	clineManifest, ok := packageManifest["cline"].(map[string]any)
	if !ok {
		t.Fatalf("Cline package manifest missing cline entry: %#v", packageManifest)
	}
	plugins, ok := clineManifest["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("Cline package plugin entries = %#v", clineManifest["plugins"])
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("Cline package plugin entry = %#v", plugins[0])
	}
	if paths, ok := entry["paths"].([]any); !ok || len(paths) != 1 || paths[0] != "./index.js" {
		t.Fatalf("Cline plugin paths = %#v", entry["paths"])
	}
	if capabilities, ok := entry["capabilities"].([]any); !ok || len(capabilities) != 1 || capabilities[0] != "hooks" {
		t.Fatalf("Cline plugin capabilities = %#v", entry["capabilities"])
	}
}

func requireClineAgentPlugin(t *testing.T, pluginDir string) {
	t.Helper()
	text := string(readTestFile(t, filepath.Join(pluginDir, "index.js"), "reading Cline AgentPlugin"))
	requireTextContainsAll(t, text, []string{
		"manifest: { capabilities: [\"hooks\"] }",
		"setup(_api, ctx)",
		"beforeRun(context)",
		"beforeTool(context)",
		"afterTool(context)",
		"afterRun({ snapshot, result })",
		"export default plugin",
		"ctx?.session?.sessionId",
		"ctx?.workspaceInfo?.rootPath",
		"snapshot.runId",
		"--pid",
		"aht_integration=cline-plugin",
		"child.once(\"error\", warnReporting)",
	}, "Cline AgentPlugin")
	if strings.Contains(text, "context.input") || strings.Contains(text, "context.result") || strings.Contains(text, "outputText") {
		t.Fatalf("Cline plugin reads content-bearing fields: %q", text)
	}
}

func requireClinePluginMarker(t *testing.T, pluginDir string) {
	t.Helper()
	marker := string(readTestFile(t, filepath.Join(pluginDir, ".aht-managed"), "reading Cline plugin marker"))
	requireTextContainsAll(t, marker, []string{
		managedMarker,
		"AHT_INTEGRATION_ID=cline",
		"AHT_INTEGRATION_VERSION=9",
		"AHT_SOURCE=cline-plugin",
	}, "Cline plugin marker")
}

func TestInstallClineRequiresForceForForeignPlugin(t *testing.T) {
	clineDir := filepath.Join(t.TempDir(), ".cline")
	t.Setenv("CLINE_DIR", clineDir)
	pluginDir := filepath.Join(clineDir, "plugins", "aht-state")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("creating Cline plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("writing foreign Cline plugin: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.HarnessCline,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged Cline plugin")
	}
}

func TestInstallClineMigratesManagedLegacyHooks(t *testing.T) {
	clineDir := filepath.Join(t.TempDir(), ".cline")
	t.Setenv("CLINE_DIR", clineDir)
	hooksDir := filepath.Join(clineDir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(hooksDir, "TaskStart.sh")
	managed := "#!/bin/sh\n# " + managedMarker + "\n# AHT_INTEGRATION_ID=cline\n# AHT_INTEGRATION_VERSION=3\n"
	if err := os.WriteFile(managedPath, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(hooksDir, "TaskResume.sh")
	if err := os.WriteFile(foreignPath, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{Harness: registry.HarnessCline, Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("migrating Cline integration: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected migration to report a change")
	}
	if _, err := os.Stat(managedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy managed hook still exists: %v", err)
	}
	if _, err := os.Stat(foreignPath); err != nil {
		t.Fatalf("foreign legacy hook was removed: %v", err)
	}
}

func TestInstallShimRequiresForceForForeignFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	path := filepath.Join(dir, "shims", "opencode")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating shim dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("writing foreign shim: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.HarnessOpenCode,
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
		DryRun:       false,
		Force:        false,
		UseShim:      true,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged shim")
	}
}

func TestInstallShimWritesManagedScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)

	result, err := Run(Options{
		Harness:      registry.HarnessOpenCode,
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
		DryRun:       false,
		Force:        false,
		UseShim:      true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected shim install to report changed")
	}
	if !strings.Contains(result.Snippet, managedMarker) {
		t.Fatalf("expected managed marker in snippet: %q", result.Snippet)
	}
	if result.Path != filepath.Join(dir, "shims", "opencode") {
		t.Fatalf("unexpected path %q", result.Path)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != shimFileMode {
		t.Fatalf("installed shim mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(shimFileMode))
	}
}

func TestInstallShimRepairsExecutableMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	options := Options{
		Harness: registry.HarnessOpenCode, Binary: defaultBinary,
		TargetBinary: "/usr/bin/opencode", UseShim: true,
	}
	first, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(first.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(registry.HarnessOpenCode, defaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != ArtifactStale {
		t.Fatalf("non-executable shim status = %q, want stale", status.Status)
	}
	repaired, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.Changed {
		t.Fatal("mode repair was not reported as a change")
	}
	info, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != shimFileMode {
		t.Fatalf("repaired shim mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(shimFileMode))
	}
}

func TestInstallShimSupportsHarnessesMissingExitHooks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		harness      registry.Harness
		targetBinary string
	}{
		{name: "codex", harness: registry.HarnessCodex, targetBinary: "/usr/bin/codex"},
		{name: "agy", harness: registry.HarnessAgy, targetBinary: "/usr/bin/agy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(registry.StateDirEnv, dir)

			result, err := Run(Options{
				Harness:      tc.harness,
				Binary:       defaultBinary,
				TargetBinary: tc.targetBinary,
				DryRun:       false,
				Force:        false,
				UseShim:      true,
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if !result.Changed {
				t.Fatal("expected shim install to report changed")
			}
			if result.Path != filepath.Join(dir, "shims", string(tc.harness)) {
				t.Fatalf("unexpected path %q", result.Path)
			}
			requireTextContainsAll(t, result.Snippet, []string{
				managedMarker,
				"harness_bin=" + tc.targetBinary,
				"report " + string(tc.harness) + " --presence live --evidence process --pid \"$$\"",
				"report " + string(tc.harness) + " --presence gone --evidence process --pid \"$$\"",
			}, "shim script")
		})
	}
}

func TestInstallShimResolvesTargetOutsideManagedShimDir(t *testing.T) {
	dir := t.TempDir()
	realDir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	t.Setenv("PATH", filepath.Join(dir, "shims")+string(os.PathListSeparator)+realDir)

	shimPath := filepath.Join(dir, "shims", "opencode")
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o700); err != nil {
		t.Fatalf("creating shim dir: %v", err)
	}
	if err := writeExecutableTestFile(shimPath, []byte("#!/bin/sh\n# "+managedMarker+"\n")); err != nil {
		t.Fatalf("writing existing shim: %v", err)
	}
	realPath := filepath.Join(realDir, "opencode")
	if err := writeExecutableTestFile(realPath, []byte("#!/bin/sh\n")); err != nil {
		t.Fatalf("writing real harness binary: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.HarnessOpenCode,
		Binary:       defaultBinary,
		TargetBinary: "",
		DryRun:       true,
		Force:        false,
		UseShim:      true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(result.Snippet, "harness_bin="+realPath) {
		t.Fatalf("expected shim to target real binary %q, got snippet: %s", realPath, result.Snippet)
	}
	if strings.Contains(result.Snippet, "harness_bin="+shimPath) {
		t.Fatalf("shim targets itself: %s", result.Snippet)
	}
}

func TestInstallShimRejectsManagedShimTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	shimPath := filepath.Join(dir, "shims", "opencode")

	_, err := Run(Options{
		Harness:      registry.HarnessOpenCode,
		Binary:       defaultBinary,
		TargetBinary: shimPath,
		DryRun:       true,
		Force:        false,
		UseShim:      true,
	})
	if !errors.Is(err, errRecursiveShimTarget) {
		t.Fatalf("expected errRecursiveShimTarget, got %v", err)
	}
}

func writeStalePiExtension(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "extensions", piExtensionName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `"aht managed integration";
"AHT_INTEGRATION_ID=pi";
"AHT_INTEGRATION_VERSION=5";
`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallPiWritesExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	path := writeStalePiExtension(t, dir)

	result, err := Run(Options{
		Harness:      registry.HarnessPi,
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
		t.Fatal("expected pi install to report changed")
	}
	if result.Path != path {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		`on("agent_start"`,
		`on("before_agent_start"`,
		`on("ui_prompt_start"`,
		`report("waiting", ctx, event)`,
		`on("ui_prompt_end"`,
		`report(ctx.isIdle?.() ? "idle" : "running", ctx, event)`,
		"AHT_INTEGRATION_ID=pi",
		"AHT_INTEGRATION_VERSION=13",
		`"report", "pi"`,
		`"--observed-at", observedAt`,
		"addEvent(args, event?.type)",
		`addAttribute(args, "pi_prompt_kind", event?.kind)`,
		`args.push("--session-id", currentSessionId)`,
		`args.push("--session-path", currentSessionPath)`,
	}, "pi extension")
	if strings.Contains(result.Snippet, `on("tool_approval_`) {
		t.Fatalf("Pi extension must use documented UI prompt events: %q", result.Snippet)
	}
}

func TestInstallOmpWritesExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessOmp,
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
		t.Fatal("expected oh-my-pi install to report changed")
	}
	if result.Path != filepath.Join(dir, "extensions", ompExtensionName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=omp",
		`on("session_start"`,
		`on("agent_end"`,
		`event?.willContinue === true`,
		`on("tool_approval_requested"`,
		`on("tool_approval_resolved"`,
		`on("tool_execution_start"`,
		`on("tool_execution_end"`,
		`on("session_stop"`,
		`on("session_shutdown"`,
		`report("idle", "live"`,
		`activity: "waiting"`,
		`queueState("interrupted"`,
		`report(undefined, "gone"`,
		`args.push("--resume-command", item)`,
		`args.push("--session-id", ref.id)`,
		`args.push("--session-path", ref.path)`,
		`args.push("--cwd", ref.cwd)`,
		`"report",`,
		`"omp",`,
		`"--sequence",`,
		`drainStateQueue`,
		`retryableErrorPattern`,
		`entry?.type === "session_init"`,
		"if (isSubagentSession(ctx)) return Promise.resolve()",
		"AHT_INTEGRATION_VERSION=15",
	}, "oh-my-pi extension")
	if strings.Contains(result.Snippet, `on("input"`) {
		t.Fatalf("OMP extension must not treat local interactive input as agent activity: %q", result.Snippet)
	}
	if strings.Contains(result.Snippet, `"--queue"`) {
		t.Fatalf("OMP extension must report through the broker hot path: %q", result.Snippet)
	}
}

func TestPiAndOmpRefuseToOverwriteSharedExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	piResult, err := Run(Options{Harness: registry.HarnessPi, Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(piResult.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{Harness: registry.HarnessOmp, Binary: testInstallBinary}); !errors.Is(err, errForeignFile) {
		t.Fatalf("OMP overwrite error = %v, want errForeignFile", err)
	}
	current, err := os.ReadFile(piResult.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) || !strings.Contains(string(current), "AHT_INTEGRATION_ID=pi") {
		t.Fatalf("Pi extension changed after refused OMP install: %s", current)
	}
}

func TestInstallOmpUsesProfileAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", ".omp")
	t.Setenv("OMP_PROFILE", "work")
	t.Setenv("PI_PROFILE", "")

	result, err := Run(Options{
		Harness:      registry.HarnessOmp,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantPath := filepath.Join(home, ".omp", "profiles", "work", "agent", "extensions", ompExtensionName)
	if result.Path != wantPath {
		t.Fatalf("unexpected profile path %q, want %q", result.Path, wantPath)
	}
}

func TestInstallOpenCodeWritesPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessOpenCode,
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
		t.Fatal("expected opencode install to report changed")
	}
	if result.Path != filepath.Join(dir, "opencode", "plugins", openCodePluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	if !strings.Contains(result.Snippet, "AHT_INTEGRATION_ID=opencode") {
		t.Fatalf("expected integration id in snippet: %q", result.Snippet)
	}
	if !strings.Contains(result.Snippet, `event: async ({ event }`) {
		t.Fatalf("expected native event handler in snippet: %q", result.Snippet)
	}
	if !strings.Contains(result.Snippet, `"permission.asked"`) {
		t.Fatalf("expected permission event mapping in snippet: %q", result.Snippet)
	}
	if !strings.Contains(result.Snippet, `"session.deleted"`) || !strings.Contains(result.Snippet, `state === "gone" ? "--presence"`) {
		t.Fatalf("expected OpenCode session deletion mapping in snippet: %q", result.Snippet)
	}
	if !strings.Contains(result.Snippet, `"--observed-at", observedAt`) {
		t.Fatalf("expected opencode observed timestamp in snippet: %q", result.Snippet)
	}
}

func TestInstallKiloWritesPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessKilo,
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
		t.Fatal("expected kilo install to report changed")
	}
	if result.Path != filepath.Join(dir, "kilo", "plugin", kiloPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=kilo",
		`export default { id: "aht-state", server: AHTPlugin };`,
		`event: async ({ event }`,
		`"permission.asked"`,
		`"session.deleted"`,
		`state === "gone" ? "--presence"`,
		`"AHT_INTEGRATION_VERSION=8"`,
		`"--observed-at", observedAt`,
		`"kilo_status"`,
		`"aht_integration", source`,
	}, "kilo snippet")
}

func TestInstallKiloReplacesManagedPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "kilo", "plugin", kiloPluginName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating kilo plugin dir: %v", err)
	}
	oldPlugin := `"aht managed integration";
const old = "old-aht";
`
	if err := os.WriteFile(path, []byte(oldPlugin), 0o600); err != nil {
		t.Fatalf("writing old plugin: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.HarnessKilo,
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
		t.Fatal("expected kilo install to replace old managed plugin")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed plugin: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed plugin to be removed: %s", text)
	}
	second, err := Run(Options{
		Harness:      registry.HarnessKilo,
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
		t.Fatal("expected second kilo install to be idempotent")
	}
}

func TestInstallAgyWritesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(Options{
		Harness:      registry.HarnessAgy,
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
		t.Fatal("expected agy install to report changed")
	}
	if result.Path != filepath.Join(home, ".gemini", "antigravity-cli", "plugins", agyPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{"hook agy"}, "agy snippet")
	requireAgyPluginManifest(t, result.Path)
	requireAgyPluginHooks(t, result.Path)
	requireAgyPluginMarker(t, result.Path)
	requireAgyImportManifest(t, filepath.Join(home, ".gemini", "antigravity-cli", agyImportManifestName))

	second, err := Run(Options{
		Harness:      registry.HarnessAgy,
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
		t.Fatal("expected second agy install to be idempotent")
	}
}

func TestInstallAgyRequiresForceForForeignPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginDir := filepath.Join(home, ".gemini", "antigravity-cli", "plugins", agyPluginName)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("creating agy plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"foreign"}`), 0o600); err != nil {
		t.Fatalf("writing foreign plugin manifest: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.HarnessAgy,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged agy plugin")
	}

	result, err := Run(Options{
		Harness:      registry.HarnessAgy,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        true,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("forced Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected forced agy install to report changed")
	}
}

func TestInstallGooseWritesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginPath := filepath.Join(home, ".agents", "plugins", goosePluginName)
	if err := os.MkdirAll(pluginPath, 0o700); err != nil {
		t.Fatal(err)
	}
	staleMarker := "aht managed integration\nAHT_INTEGRATION_ID=goose\nAHT_INTEGRATION_VERSION=5\n"
	if err := os.WriteFile(filepath.Join(pluginPath, gooseMarkerFileName), []byte(staleMarker), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{
		Harness:      registry.HarnessGoose,
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
		t.Fatal("expected goose install to report changed")
	}
	if result.Path != pluginPath {
		t.Fatalf("unexpected path %q", result.Path)
	}

	requireGoosePluginManifest(t, result.Path)
	requireGoosePluginHooks(t, result.Path)
	requireGoosePluginScript(t, result.Path)
	requireGoosePluginMarker(t, result.Path)
	marker := readTestFile(t, filepath.Join(pluginPath, gooseMarkerFileName), "reading updated Goose marker")
	if !strings.Contains(string(marker), "AHT_INTEGRATION_VERSION=7") {
		t.Fatalf("stale Goose integration was not updated: %s", marker)
	}

	second, err := Run(Options{
		Harness:      registry.HarnessGoose,
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
		t.Fatal("expected second goose install to be idempotent")
	}
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
		Harness:      registry.HarnessDroid,
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
		"aht_integration=droid-hook",
		"aht_integration_version=7",
	}, "droid hooks")
	if strings.Contains(text, "statusMessage") {
		t.Fatalf("expected Droid hooks not to include unsupported statusMessage field: %s", text)
	}

	second, err := Run(Options{
		Harness:      registry.HarnessDroid,
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

func TestInstallKimiCodeWritesHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
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
		t.Fatal("expected kimi-code install to report changed")
	}
	if result.Path != filepath.Join(dir, "config.toml") {
		t.Fatalf("unexpected path %q", result.Path)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}
	text := string(data)
	for _, event := range []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		hookEventStop,
		"StopFailure",
		"SubagentStart",
		"SubagentStop",
		"PreCompact",
		"PostCompact",
		"Notification",
		"SessionEnd",
	} {
		if !strings.Contains(text, `event = "`+event+`"`) {
			t.Fatalf("expected %s hook in snippet: %s", event, text)
		}
	}
	for _, want := range []string{
		`matcher = "startup|resume"`,
		`matcher = "permission_prompt"`,
		`event = "SessionEnd"` + "\nmatcher = \"exit\"",
		"--raw-stdin",
		"--quiet",
		"aht_integration=kimi-code-hook",
		"aht_integration_version=7",
		managedMarker,
		"--activity idle --event SessionStart",
		"--activity running --event UserPromptSubmit",
		"--activity running --event PreToolUse",
		"--activity waiting --event Notification",
		"--activity failed --event StopFailure",
		"--presence gone --event SessionEnd",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in snippet: %s", want, text)
		}
	}
	for _, forbidden := range []string{"statusMessage", "hooks =", "type ="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unexpected unsupported Kimi hook field %q in snippet: %s", forbidden, text)
		}
	}
}

func TestInstallKimiCodeReplacesManagedBlockAndPreservesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating kimi-code dir: %v", err)
	}
	oldConfig := strings.Join([]string{
		`default_model = "kimi-code/kimi-for-coding"`,
		"",
		kimiCodeManagedStart,
		"[[hooks]]",
		`event = "` + hookEventSessionStart + `"`,
		`command = "old-aht report --harness kimi-code --source kimi-code-hook"`,
		kimiCodeManagedEnd,
		"",
		"[thinking]",
		`mode = "auto"`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old config: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
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
		t.Fatal("expected kimi-code install to replace old managed block")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}
	text := string(data)
	for _, want := range []string{`default_model = "kimi-code/kimi-for-coding"`, "[thinking]", `mode = "auto"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected preserved config %q in snippet: %s", want, text)
		}
	}
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}

	second, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
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
		t.Fatal("expected second kimi-code install to be idempotent")
	}
}

func TestInstallKimiCodeDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       true,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kimi-code dry-run to report changed")
	}
	if !strings.Contains(result.Snippet, `event = "`+hookEventSessionStart+`"`) {
		t.Fatalf("expected dry-run snippet to include Kimi hooks: %s", result.Snippet)
	}
	if _, err := os.Stat(result.Path); err == nil {
		t.Fatalf("expected dry-run not to write %s", result.Path)
	}
}

func TestInstallGrokWritesHooks(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())

	result, err := Run(Options{
		Harness:      registry.HarnessGrok,
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
	if !strings.Contains(text, "aht_integration=grok-hook") {
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
		Harness:      registry.HarnessGrok,
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
		Harness:      registry.HarnessGrok,
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

func TestRunAllInstallsEveryHarness(t *testing.T) {
	installFakeOpenClawCLI(t)
	installFakeHermesCLI(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	t.Setenv("KIMI_SHARE_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv(registry.StateDirEnv, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AGY_CLI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	harnesses := AllHarnesses()
	results := make([]Result, 0, len(harnesses))
	for _, h := range harnesses {
		res, err := RunContext(context.Background(), Options{
			Harness:      h,
			Binary:       defaultBinary,
			TargetBinary: "/usr/bin/opencode",
			DryRun:       false,
			Force:        false,
			UseShim:      false,
		})
		if err != nil {
			t.Fatalf("RunContext for %s returned error: %v", h, err)
		}
		results = append(results, res)
	}

	for _, result := range results {
		if result.Error != "" {
			t.Fatalf("unexpected result error for %s: %s", result.Harness, result.Error)
		}
		if result.Path == "" {
			t.Fatalf("expected path for %s", result.Harness)
		}
	}
}

func TestInstallPlansMatchHarnessCatalog(t *testing.T) {
	t.Parallel()

	for _, adapter := range harnesscatalog.All() {
		if _, ok := adapter.(harnesspkg.Installable); !ok {
			t.Fatalf("harness %q has no install plan", adapter.Definition().ID)
		}
	}

	for _, harness := range AllHarnesses() {
		adapter, ok := harnesscatalog.Find(harness)
		if !ok {
			t.Fatalf("AllHarnesses contains unknown harness %q", harness)
		}
		if _, installable := adapter.(harnesspkg.Installable); !installable {
			t.Fatalf("AllHarnesses contains %q without install plan", harness)
		}
	}
}

func TestAllHarnesses(t *testing.T) {
	t.Parallel()

	harnesses := AllHarnesses()
	if len(harnesses) == 0 {
		t.Fatal("expected installable harnesses")
	}
	if !slices.Contains(harnesses, registry.HarnessCodex) {
		t.Fatalf("AllHarnesses() = %v, want codex", harnesses)
	}
}

type managedReplacementCase struct {
	Harness              registry.Harness
	Path                 string
	RemovedText          string
	RequiredText         []string
	FirstChangeMessage   string
	SecondChangedMessage string
	ExpectedNextStep     string
}

func requireManagedReplacement(t *testing.T, test managedReplacementCase) {
	t.Helper()

	result, err := Run(Options{
		Harness:      test.Harness,
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
		t.Fatal(test.FirstChangeMessage)
	}
	if result.NextStep != test.ExpectedNextStep {
		t.Fatalf("install next step = %q, want %q", result.NextStep, test.ExpectedNextStep)
	}

	text := string(readTestFile(t, test.Path, "reading installed hooks"))
	if strings.Contains(text, test.RemovedText) {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}
	requireTextContainsAll(t, text, test.RequiredText, "installed hooks")

	second, err := Run(Options{
		Harness:      test.Harness,
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
		t.Fatal(test.SecondChangedMessage)
	}
	if second.NextStep != "" {
		t.Fatalf("idempotent install unexpectedly requires activation: %q", second.NextStep)
	}
}

func readTestFile(t *testing.T, path string, context string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}

	return data
}

func decodeTestJSONObject(t *testing.T, data []byte, context string) map[string]any {
	t.Helper()

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON for %s: %v", context, err)
	}

	return config
}

func requireTestHooks(t *testing.T, config map[string]any) map[string]any {
	t.Helper()

	hooks, hooksOK := config["hooks"].(map[string]any)
	if !hooksOK {
		t.Fatal("expected hooks object")
	}

	return hooks
}

func requireTestHookEvents(t *testing.T, hooks map[string]any, events []string) {
	t.Helper()

	for _, event := range events {
		if _, hasEvent := hooks[event]; !hasEvent {
			t.Fatalf("expected %s hook", event)
		}
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

func requireTestHookTimeout(t *testing.T, hooks map[string]any, event string) float64 {
	t.Helper()
	handler := requireTestHookHandler(t, hooks, event)
	timeout, ok := handler["timeout"].(float64)
	if !ok {
		t.Fatalf("expected %s hook timeout, got %#v", event, handler["timeout"])
	}
	return timeout
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

func requireTextContainsAll(t *testing.T, text string, values []string, context string) {
	t.Helper()

	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("expected %q in %s: %s", value, context, text)
		}
	}
}

func requireAgyPluginManifest(t *testing.T, dir string) {
	t.Helper()

	manifestData := readTestFile(t, filepath.Join(dir, "plugin.json"), "reading plugin manifest")
	manifest := decodeTestJSONObject(t, manifestData, "plugin manifest")
	if manifest["name"] != agyPluginName {
		t.Fatalf("expected plugin name %q, got %#v", agyPluginName, manifest["name"])
	}
}

func requireAgyPluginHooks(t *testing.T, dir string) {
	t.Helper()

	hooksData := readTestFile(t, filepath.Join(dir, "hooks.json"), "reading agy hooks")
	if !strings.Contains(string(hooksData), testInstallBinary+" --json hook agy") {
		t.Fatalf("expected agy hooks to request protocol JSON explicitly: %s", hooksData)
	}
	hooks := decodeTestJSONObject(t, hooksData, "agy hooks")
	pluginHooks, hooksOK := hooks[agyPluginName].(map[string]any)
	if !hooksOK {
		t.Fatalf("expected %s hook namespace, got %#v", agyPluginName, hooks)
	}
	requireTestHookEvents(t, pluginHooks, []string{"PreInvocation", "PostInvocation", "PreToolUse", "PostToolUse", hookEventStop})
}

func requireAgyPluginMarker(t *testing.T, dir string) {
	t.Helper()

	marker := readTestFile(t, filepath.Join(dir, agyMarkerFileName), "reading agy marker")
	if !strings.Contains(string(marker), managedMarker) {
		t.Fatalf("expected managed marker, got %q", marker)
	}
	if !strings.Contains(string(marker), "AHT_INTEGRATION_VERSION=8") {
		t.Fatalf("expected agy integration version 8 marker, got %q", marker)
	}
}

func requireAgyImportManifest(t *testing.T, path string) {
	t.Helper()

	data := readTestFile(t, path, "reading agy import manifest")
	manifest := decodeTestJSONObject(t, data, "agy import manifest")
	imports, importsOK := manifest["imports"].([]any)
	if !importsOK {
		t.Fatalf("expected agy imports list, got %#v", manifest)
	}

	for _, importValue := range imports {
		importItem, importOK := importValue.(map[string]any)
		if !importOK || importItem["name"] != agyPluginName {
			continue
		}
		if importItem["source"] != agyImportSource {
			t.Fatalf("expected agy import source %q, got %#v", agyImportSource, importItem["source"])
		}
		components, componentsOK := importItem["components"].([]any)
		if !componentsOK {
			t.Fatalf("expected agy import components, got %#v", importItem["components"])
		}
		for _, component := range components {
			if component == agyImportComponent {
				return
			}
		}
		t.Fatalf("expected agy import component %q, got %#v", agyImportComponent, components)
	}

	t.Fatalf("expected agy import for %q, got %#v", agyPluginName, imports)
}

func requireGoosePluginManifest(t *testing.T, dir string) {
	t.Helper()

	manifestData := readTestFile(t, filepath.Join(dir, "plugin.json"), "reading goose plugin manifest")
	manifest := decodeTestJSONObject(t, manifestData, "goose plugin manifest")
	if manifest["name"] != goosePluginName {
		t.Fatalf("expected plugin name %q, got %#v", goosePluginName, manifest["name"])
	}
	if manifest["description"] != managedMarker {
		t.Fatalf("expected managed marker description, got %#v", manifest["description"])
	}
}

func requireGoosePluginHooks(t *testing.T, dir string) {
	t.Helper()

	hooksData := readTestFile(t, filepath.Join(dir, "hooks", "hooks.json"), "reading goose hooks")
	hooksConfig := decodeTestJSONObject(t, hooksData, "goose hooks")
	hooks := requireTestHooks(t, hooksConfig)
	requireTestHookEvents(t, hooks, []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"BeforeReadFile",
		"AfterFileEdit",
		"BeforeShellExecution",
		"AfterShellExecution",
		hookEventStop,
		"SessionEnd",
	})

	text := string(hooksData)
	requireTextContainsAll(t, text, []string{
		"${PLUGIN_ROOT}/scripts/report.sh",
	}, "goose hooks")
}

func requireGoosePluginScript(t *testing.T, dir string) {
	t.Helper()

	text := string(readTestFile(t, filepath.Join(dir, "scripts", "report.sh"), "reading goose report script"))
	requireTextContainsAll(t, text, []string{
		managedMarker,
		"--raw-stdin-defaults-only",
		"aht_integration=goose-hook",
		"aht_integration_version=7",
		`--presence "$transition"`,
		`--activity "$transition"`,
		`--event "$event"`,
	}, "goose report script")
}

func requireGoosePluginMarker(t *testing.T, dir string) {
	t.Helper()

	marker := readTestFile(t, filepath.Join(dir, gooseMarkerFileName), "reading goose marker")
	if !strings.Contains(string(marker), managedMarker) {
		t.Fatalf("expected managed marker, got %q", marker)
	}
}

func writeExecutableTestFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing executable test file: %w", err)
	}

	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("marking executable test file executable: %w", err)
	}

	return nil
}
