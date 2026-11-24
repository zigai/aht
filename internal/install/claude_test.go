package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	claudePluginID      = "aht-state@aht"
	claudeMarketplace   = "aht"
	claudeModulePath    = "aht-state/hooks/register.ts"
	claudeSettingsFile  = "settings.json"
	claudeLegacyBinary  = "'/old/bin/renamed tracker'"
	claudeUserNotifier  = "notify-send turn-finished"
	claudeUserLookalike = "echo '/usr/local/bin/aht --json hook claude --event FileChanged'"
	claudeUserShell     = "sh -c '/usr/local/bin/aht --json hook claude --event FileChanged'"
)

type claudeModuleCommand struct {
	Event   string   `json:"event"`
	Matcher string   `json:"matcher"`
	Args    []string `json:"args"`
}

func claudeConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	return dir
}

func installClaude(t *testing.T, opts Options) Result {
	t.Helper()

	opts.Harness = registry.Harness("claude")
	if opts.Binary == "" {
		opts.Binary = testInstallBinary
	}

	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatalf("installing claude plugin: %v", err)
	}

	return result
}

func claudeStatus(t *testing.T) ArtifactStatus {
	t.Helper()

	status, err := Inspect(t.Context(), registry.Harness("claude"), testInstallBinary)
	if err != nil {
		t.Fatalf("inspecting claude integration: %v", err)
	}

	return status.Status
}

func writeClaudeSettings(t *testing.T, dir string, config map[string]any) []byte {
	t.Helper()

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, claudeSettingsFile), data, 0o600); err != nil {
		t.Fatal(err)
	}

	return data
}

func readClaudeSettings(t *testing.T, dir string) map[string]any {
	t.Helper()

	data := readTestFile(t, filepath.Join(dir, claudeSettingsFile), "reading claude settings")

	return decodeTestJSONObject(t, data, "claude settings")
}

func claudeObject(t *testing.T, value any, description string) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %#v", description, value)
	}

	return object
}

func commandHookGroup(matcher string, commands ...string) map[string]any {
	handlers := make([]any, 0, len(commands))
	for _, command := range commands {
		handlers = append(handlers, map[string]any{"type": "command", "command": command})
	}

	group := map[string]any{"hooks": handlers}
	if matcher != "" {
		group["matcher"] = matcher
	}

	return group
}

func TestClaudeInstallWritesPluginAndRegistersIt(t *testing.T) {
	dir := claudeConfigDir(t)
	writeClaudeSettings(t, dir, map[string]any{
		"theme":          "dark",
		"enabledPlugins": map[string]any{"other@market": true},
		"extraKnownMarketplaces": map[string]any{
			"market": map[string]any{
				"source": map[string]any{"source": "github", "repo": "someone/market"},
			},
		},
	})

	if status := claudeStatus(t); status != ArtifactMissing {
		t.Fatalf("status before install = %s, want missing", status)
	}

	result := installClaude(t, Options{})
	if !result.Changed || result.Path != filepath.Join(dir, claudeMarketplace) {
		t.Fatalf("install result = %+v", result)
	}

	for _, name := range []string{
		".claude-plugin/marketplace.json",
		"aht-state/.claude-plugin/plugin.json",
		"aht-state/hooks/hooks.json",
		claudeModulePath,
		".aht-managed",
	} {
		if _, err := os.Stat(filepath.Join(result.Path, name)); err != nil {
			t.Fatalf("expected plugin file %s: %v", name, err)
		}
	}

	marketplace := decodeTestJSONObject(t, readTestFile(
		t, filepath.Join(result.Path, ".claude-plugin", "marketplace.json"), "reading marketplace",
	), "marketplace")

	plugins, ok := marketplace["plugins"].([]any)
	if marketplace["name"] != claudeMarketplace || !ok || len(plugins) != 1 ||
		claudeObject(t, plugins[0], "marketplace plugin")["source"] != "./aht-state" {
		t.Fatalf("marketplace does not list the plugin folder: %#v", marketplace)
	}

	hooks := decodeTestJSONObject(t, readTestFile(
		t, filepath.Join(result.Path, "aht-state", "hooks", "hooks.json"), "reading hooks.json",
	), "hooks.json")
	if !reflect.DeepEqual(hooks["modules"], []any{"./register.ts"}) {
		t.Fatalf("hooks.json = %#v, want the register.ts module", hooks)
	}

	settings := readClaudeSettings(t, dir)

	want := map[string]any{
		"theme": "dark",
		"enabledPlugins": map[string]any{
			"other@market": true,
			claudePluginID: true,
		},
		"extraKnownMarketplaces": map[string]any{
			"market": map[string]any{
				"source": map[string]any{"source": "github", "repo": "someone/market"},
			},
			claudeMarketplace: map[string]any{
				"source": map[string]any{"source": "directory", "path": result.Path},
			},
		},
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings = %#v, want %#v", settings, want)
	}

	if status := claudeStatus(t); status != ArtifactCurrent {
		t.Fatalf("status after install = %s, want current", status)
	}

	if installClaude(t, Options{}).Changed {
		t.Fatal("expected second claude install to be idempotent")
	}
}

func TestClaudePluginReportsMainSessionActivity(t *testing.T) {
	claudeConfigDir(t)
	result := installClaude(t, Options{})

	source := string(
		readTestFile(t, filepath.Join(result.Path, claudeModulePath), "reading module"),
	)
	requireTextContainsAll(t, source, []string{
		"// " + managedMarker,
		"const binary = " + `"` + testInstallBinary + `"`,
	}, "claude module")

	commands := claudeModuleCommands(t, source)
	hooks := make(map[string]any)

	for _, command := range commands {
		if !strings.Contains(source, `on("classic.`+command.Event+`"`) {
			t.Fatalf("module registers no hook for %s", command.Event)
		}

		groups, _ := hooks[command.Event].([]any)
		hooks[command.Event] = append(groups, commandHookGroup(
			command.Matcher,
			testInstallBinary+" "+strings.Join(command.Args, " "),
		))
	}

	requireTestHookEvents(t, hooks, []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"PermissionRequest",
		"PermissionDenied",
		"Elicitation",
		"ElicitationResult",
		"Notification",
		"PreCompact",
		"PostCompact",
		hookEventStop,
		"StopFailure",
		"SessionEnd",
	})
	requireNoSubagentHooks(t, hooks, "SubagentStart", "SubagentStop")
	requireCompactionKeepsTurnRunning(t, hooks, "startup|resume|clear")
	requireManualPostCompactIdle(t, hooks, "manual")
	requireOnlyManualPreCompactRunning(t, hooks)

	if !slices.ContainsFunc(commands, func(command claudeModuleCommand) bool {
		return command.Event == "PermissionDenied" &&
			slices.Contains(command.Args, "running") &&
			!slices.Contains(command.Args, "idle")
	}) {
		t.Fatalf("PermissionDenied does not report running: %#v", commands)
	}

	if !slices.ContainsFunc(commands, func(command claudeModuleCommand) bool {
		return command.Event == "Notification" &&
			command.Matcher == "permission_prompt|elicitation_dialog|elicitation_url_dialog"
	}) {
		t.Fatalf("Notification matcher does not cover elicitation dialogs: %#v", commands)
	}

	for _, command := range commands {
		report := len(command.Args) > 0 && command.Args[0] == "report"
		if report && !slices.Contains(command.Args, "--raw-stdin") {
			t.Fatalf("%s report does not pass the hook input: %v", command.Event, command.Args)
		}
	}

	for _, event := range []string{hookEventSessionStart, hookEventStop} {
		title := []string{"--json", "hook", "claude", "--event", event}
		if !slices.ContainsFunc(commands, func(command claudeModuleCommand) bool {
			return command.Event == event && command.Matcher == "" &&
				reflect.DeepEqual(command.Args, title)
		}) {
			t.Fatalf("no title refresh on %s: %#v", event, commands)
		}
	}
}

func claudeModuleCommands(t *testing.T, source string) []claudeModuleCommand {
	t.Helper()

	const prefix = "const commands: Command[] = "

	for line := range strings.Lines(source) {
		encoded, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
		if !ok {
			continue
		}

		var commands []claudeModuleCommand
		if err := json.Unmarshal([]byte(strings.TrimSuffix(encoded, ";")), &commands); err != nil {
			t.Fatalf("decoding module commands: %v", err)
		}

		return commands
	}

	t.Fatalf("module has no command table:\n%s", source)

	return nil
}

func TestClaudeInstallReplacesSettingsHooks(t *testing.T) {
	dir := claudeConfigDir(t)
	userGroup := commandHookGroup("", claudeUserNotifier)
	lookalikes := commandHookGroup("user.txt", claudeUserLookalike, claudeUserShell)
	before := writeClaudeSettings(t, dir, map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"SessionStart": []any{
				commandHookGroup(
					"startup|resume|clear",
					claudeLegacyBinary+" report claude --activity idle --event SessionStart "+
						"--reporter-version 13 --reporter claude-hook --raw-stdin --quiet",
				),
				commandHookGroup("", claudeLegacyBinary+" --json hook claude --event SessionStart"),
			},
			"SubagentStop": []any{commandHookGroup(
				"",
				"aht report claude --activity idle --event SubagentStop --reporter-version 9 "+
					"--reporter claude-hook --raw-stdin --quiet",
			)},
			"Stop": []any{
				commandHookGroup(
					"",
					"old-aht report --harness claude --state idle --source claude-hook "+
						"--attribute aht_integration=claude-hook",
				),
				userGroup,
			},
			"FileChanged": []any{
				commandHookGroup(
					"user.txt",
					claudeLegacyBinary+" --json hook claude --event FileChanged",
					claudeUserLookalike,
					claudeUserShell,
				),
			},
		},
	})

	if status := claudeStatus(t); status != ArtifactPartial {
		t.Fatalf("status with settings hooks = %s, want partial", status)
	}

	if dryRun := upgradeClaude(t, true); !dryRun.Changed {
		t.Fatal("expected settings hooks to make the dry run report a change")
	}

	if data := readTestFile(
		t,
		filepath.Join(dir, claudeSettingsFile),
		"reading settings",
	); string(
		data,
	) != string(
		before,
	) {
		t.Fatalf("dry run changed settings:\n%s", data)
	}

	if _, err := os.Stat(filepath.Join(dir, claudeMarketplace)); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the plugin: %v", err)
	}

	if !upgradeClaude(t, false).Changed {
		t.Fatal("expected upgrade to replace the settings hooks")
	}

	settings := readClaudeSettings(t, dir)

	wantHooks := map[string]any{
		"Stop":        []any{userGroup},
		"FileChanged": []any{lookalikes},
	}
	if settings["theme"] != "dark" || !reflect.DeepEqual(settings["hooks"], wantHooks) {
		t.Fatalf(
			"hooks after install = %#v, want only the user hooks %#v",
			settings["hooks"],
			wantHooks,
		)
	}

	if status := claudeStatus(t); status != ArtifactCurrent {
		t.Fatalf("status after install = %s, want current", status)
	}

	if installClaude(t, Options{}).Changed {
		t.Fatal("expected second claude install to be idempotent")
	}
}

func upgradeClaude(t *testing.T, dryRun bool) Result {
	t.Helper()

	results, err := UpgradeHarness(
		t.Context(),
		registry.Harness("claude"),
		UpgradeOptions{Binary: testInstallBinary, DryRun: dryRun},
	)
	if err != nil || len(results) != 1 {
		t.Fatalf("upgrading claude integration = %+v, %v", results, err)
	}

	return results[0]
}

func TestClaudeRepairsDisabledPlugin(t *testing.T) {
	dir := claudeConfigDir(t)
	installClaude(t, Options{})

	settings := readClaudeSettings(t, dir)
	claudeObject(t, settings["enabledPlugins"], "enabledPlugins")[claudePluginID] = false
	settings["hooks"] = map[string]any{
		"SessionStart": []any{
			commandHookGroup("", claudeLegacyBinary+" --json hook claude --event SessionStart"),
		},
	}
	writeClaudeSettings(t, dir, settings)

	if status := claudeStatus(t); status != ArtifactDisabled {
		t.Fatalf("status of disabled plugin = %s, want disabled", status)
	}

	if result := upgradeClaude(t, false); result.Changed {
		t.Fatalf("upgrade changed disabled registration: %+v", result)
	}

	plugins := claudeObject(t, readClaudeSettings(t, dir)["enabledPlugins"], "enabledPlugins")
	if got := plugins[claudePluginID]; got != false {
		t.Fatalf("plugin enabled after upgrade = %#v, want false", got)
	}

	if !installClaude(t, Options{}).Changed {
		t.Fatal("expected install to enable the disabled plugin")
	}

	enabled := claudeObject(t, readClaudeSettings(t, dir)["enabledPlugins"], "enabledPlugins")[claudePluginID]
	if enabled != true {
		t.Fatalf("plugin enabled = %#v, want true", enabled)
	}
}

func TestClaudeRefusesForeignMarketplace(t *testing.T) {
	dir := claudeConfigDir(t)
	before := writeClaudeSettings(t, dir, map[string]any{
		"extraKnownMarketplaces": map[string]any{
			claudeMarketplace: map[string]any{
				"source": map[string]any{"source": "github", "repo": "someone/aht"},
			},
		},
	})

	if status := claudeStatus(t); status != ArtifactForeign {
		t.Fatalf("status with a foreign marketplace = %s, want foreign", status)
	}

	_, err := Run(
		t.Context(),
		Options{Harness: registry.Harness("claude"), Binary: testInstallBinary},
	)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("install over a foreign marketplace = %v, want a --force refusal", err)
	}

	if data := readTestFile(
		t,
		filepath.Join(dir, claudeSettingsFile),
		"reading settings",
	); string(
		data,
	) != string(
		before,
	) {
		t.Fatalf("refused install changed settings:\n%s", data)
	}

	result := installClaude(t, Options{Force: true})

	marketplaces := readClaudeSettings(t, dir)["extraKnownMarketplaces"]
	source := claudeObject(t, marketplaces, "extraKnownMarketplaces")[claudeMarketplace]

	want := map[string]any{"source": map[string]any{"source": "directory", "path": result.Path}}
	if !result.Changed || !reflect.DeepEqual(source, want) {
		t.Fatalf("forced install = %+v, marketplace %#v", result, source)
	}
}

func TestClaudeRefusesSettingsWithUnexpectedShape(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings string
	}{
		{name: "plugins string", settings: `{"enabledPlugins": "aht-state@aht"}`},
		{name: "marketplaces list", settings: `{"extraKnownMarketplaces": []}`},
		{name: "invalid json", settings: `{"theme": `},
		{name: "trailing data", settings: `{} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := claudeConfigDir(t)

			path := filepath.Join(dir, claudeSettingsFile)
			if err := os.WriteFile(path, []byte(test.settings), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Run(
				t.Context(),
				Options{Harness: registry.Harness("claude"), Binary: testInstallBinary},
			)
			if err == nil {
				t.Fatal("expected install to refuse the settings")
			}

			if data := readTestFile(t, path, "reading settings"); string(data) != test.settings {
				t.Fatalf("refused install changed settings:\n%s", data)
			}

			if _, err := os.Stat(filepath.Join(dir, claudeMarketplace)); !os.IsNotExist(err) {
				t.Fatalf("refused install left the plugin behind: %v", err)
			}
		})
	}
}

func TestClaudeRemoveRestoresUserSettings(t *testing.T) {
	dir := claudeConfigDir(t)
	user := map[string]any{
		"theme":          "dark",
		"enabledPlugins": map[string]any{"other@market": true},
		"hooks":          map[string]any{"Stop": []any{commandHookGroup("", claudeUserNotifier)}},
	}
	writeClaudeSettings(t, dir, user)

	installed := installClaude(t, Options{})
	options := Options{Harness: registry.Harness("claude"), Binary: testInstallBinary}

	removed, err := Remove(t.Context(), options)
	if err != nil || !removed.Changed {
		t.Fatalf("removal = %+v, %v", removed, err)
	}

	if settings := readClaudeSettings(t, dir); !reflect.DeepEqual(settings, user) {
		t.Fatalf("settings after removal = %#v, want %#v", settings, user)
	}

	if _, err := os.Stat(installed.Path); !os.IsNotExist(err) {
		t.Fatalf("plugin still exists after removal: %v", err)
	}

	if status := claudeStatus(t); status != ArtifactMissing {
		t.Fatalf("status after removal = %s, want missing", status)
	}

	if again, err := Remove(t.Context(), options); err != nil || again.Changed {
		t.Fatalf("second removal = %+v, %v", again, err)
	}
}

func TestClaudeInstallPreservesUserHookMentioningReporterFlags(t *testing.T) {
	dir := claudeConfigDir(t)
	userHook := commandHookGroup("", "sh -c 'echo --reporter claude-hook --source claude-hook'")
	writeClaudeSettings(t, dir, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{userHook},
		},
	})

	installClaude(t, Options{})

	settings := readClaudeSettings(t, dir)

	wantHooks := map[string]any{
		"Stop": []any{userHook},
	}
	if !reflect.DeepEqual(settings["hooks"], wantHooks) {
		t.Fatalf(
			"hooks after install = %#v, want user hook preserved %#v",
			settings["hooks"],
			wantHooks,
		)
	}
}

func TestUpgradeRecoversRegisteredMarkerlessClaudePlugin(t *testing.T) {
	claudeConfigDir(t)
	installed := installClaude(t, Options{})

	marker := filepath.Join(installed.Path, ".aht-managed")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// Registration is still present; interrupt an already authorized repair
	// after backing up its markerless plugin directory.
	plugin := pluginDirectoryInstall{dir: installed.Path}

	backup, exists, err := plugin.backupCurrent(filepath.Dir(installed.Path))
	if err != nil || !exists {
		t.Fatalf("backing up registered plugin = %q, %t, %v", backup, exists, err)
	}

	id := registry.Harness("claude")

	preview, err := UpgradeHarness(
		t.Context(),
		id,
		UpgradeOptions{Binary: testInstallBinary, DryRun: true},
	)
	if err != nil || len(preview) != 1 || !preview[0].Changed {
		t.Fatalf("registered plugin recovery preview = %+v, %v; want Changed", preview, err)
	}

	if _, err := os.Lstat(installed.Path); !os.IsNotExist(err) {
		t.Fatalf("dry run changed the target: %v", err)
	}

	result, err := UpgradeHarness(t.Context(), id, UpgradeOptions{Binary: testInstallBinary})
	if err != nil || len(result) != 1 || !result[0].Changed {
		t.Fatalf("registered plugin recovery = %+v, %v; want Changed", result, err)
	}

	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("recovery left its own backup: %v", err)
	}

	if status := claudeStatus(t); status != ArtifactCurrent {
		t.Fatalf("Claude status after recovery = %v, want current", status)
	}
}
