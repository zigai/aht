package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestContextAwareIntegrationEntryPointsPreserveCancellation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := Options{Harness: registry.Harness("codex"), Binary: testInstallBinary}

	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "install", run: func() error { _, err := Run(ctx, options); return err }},
		{name: "remove", run: func() error { _, err := Remove(ctx, options); return err }},
		{name: "inspect", run: func() error { _, err := Inspect(ctx, options.Harness, options.Binary); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("context-aware %s error = %v, want context.Canceled", test.name, err)
			}
		})
	}
}

func TestRunAllInstallsEveryHarness(t *testing.T) {
	installFakeOpenClawCLI(t)
	installFakeHermesCLI(t)
	installFakeCodexCLI(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv(registry.StateDirEnv, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AGY_CLI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	harnesses := Harnesses()
	results := make([]Result, 0, len(harnesses))
	for _, h := range harnesses {
		res, err := Run(context.Background(), Options{
			Harness:      h,
			Binary:       defaultBinary,
			TargetBinary: "/usr/bin/opencode",
			DryRun:       false,
			Force:        false,
			UseShim:      false,
		})
		if err != nil {
			t.Fatalf("Run for %s returned error: %v", h, err)
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

	for _, adapter := range catalog.All() {
		if _, ok := adapter.(harnesspkg.Installable); !ok {
			t.Fatalf("harness %q has no install plan", adapter.Definition().ID)
		}
	}

	for _, harness := range Harnesses() {
		adapter, ok := catalog.Find(harness)
		if !ok {
			t.Fatalf("Harnesses contains unknown harness %q", harness)
		}
		if _, installable := adapter.(harnesspkg.Installable); !installable {
			t.Fatalf("Harnesses contains %q without install plan", harness)
		}
	}
}

// withoutHome clears the whole environment, so no home directory and no
// adapter override is known, and moves the working directory into an empty
// directory where any path built from a missing home would land.
func withoutHome(t *testing.T) string {
	t.Helper()
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); name != "" {
			t.Setenv(name, "")
		}
	}
	workDir := t.TempDir()
	t.Chdir(workDir)

	return workDir
}

func TestInstallPlansWithoutHomeFailOrStayAbsolute(t *testing.T) {
	withoutHome(t)

	for _, harness := range Harnesses() {
		t.Run(string(harness), func(t *testing.T) {
			plan, _, err := installPlanForHarness(harness, testInstallBinary)
			if err != nil {
				if !errors.Is(err, harnesspkg.ErrHomeUnknown) {
					t.Fatalf("plan error = %v, want %v", err, harnesspkg.ErrHomeUnknown)
				}
				return
			}
			for _, path := range planPaths(plan) {
				if !filepath.IsAbs(path) {
					t.Errorf("plan path %q is relative without a home directory", path)
				}
			}
		})
	}
}

func TestIntegrationEntryPointsWithoutHomeFailWithoutTouchingWorkingDirectory(t *testing.T) {
	workDir := withoutHome(t)
	options := Options{Harness: registry.Harness("claude"), Binary: testInstallBinary}

	for _, test := range []struct {
		name string
		run  func() error
	}{
		{name: "install", run: func() error { _, err := Run(t.Context(), options); return err }},
		{name: "remove", run: func() error { _, err := Remove(t.Context(), options); return err }},
		{name: "inspect", run: func() error { _, err := Inspect(t.Context(), options.Harness, options.Binary); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, harnesspkg.ErrHomeUnknown) {
				t.Fatalf("%s error = %v, want %v", test.name, err, harnesspkg.ErrHomeUnknown)
			}
		})
	}
	entries, err := os.ReadDir(workDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("working directory entries = %v, %v, want none", entries, err)
	}
}

func TestInstallPlanFollowsEnvironmentOverrideWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
	}{
		{registry.Harness("claude"), "CLAUDE_CONFIG_DIR"},
		{registry.Harness("codex"), "CODEX_HOME"},
		{registry.Harness("copilot"), "COPILOT_HOME"},
		{registry.Harness("cline"), "CLINE_DIR"},
		{registry.Harness("grok"), "GROK_HOME"},
		{registry.Harness("hermes"), "HERMES_HOME"},
		{registry.Harness("kimi-code"), "KIMI_CODE_HOME"},
		{registry.Harness("pi"), "PI_CODING_AGENT_DIR"},
		{registry.Harness("omp"), "PI_CODING_AGENT_DIR"},
		{registry.Harness("kilo"), "KILO_CONFIG_DIR"},
		{registry.Harness("opencode"), "OPENCODE_CONFIG_DIR"},
		{registry.Harness("amp"), "XDG_CONFIG_HOME"},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			withoutHome(t)
			override := t.TempDir()
			t.Setenv(test.variable, override)

			plan, _, err := installPlanForHarness(test.harness, testInstallBinary)
			if err != nil {
				t.Fatalf("plan with %s set and no home: %v", test.variable, err)
			}
			paths := planPaths(plan)
			if len(paths) == 0 {
				t.Fatal("plan has no paths")
			}
			for _, path := range paths {
				if !strings.HasPrefix(path, override+string(filepath.Separator)) {
					t.Errorf("plan path %q is outside %s=%q", path, test.variable, override)
				}
			}
		})
	}
}

func TestNoInstallRequiredIntegrationManagementIsArtifactFree(t *testing.T) {
	isolateUpgradeHome(t)
	stateDir := t.TempDir()
	t.Setenv(registry.StateDirEnv, stateDir)
	commandDir := t.TempDir()
	calledPath := filepath.Join(commandDir, "called")
	commandPath := filepath.Join(commandDir, "prime-agent")
	command := "#!/bin/sh\nprintf called > " + calledPath + "\nexit 97\n"
	if err := writeExecutableTestFile(commandPath, []byte(command)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", commandDir)

	const label = "native session catalog"
	result, err := Run(t.Context(), Options{Harness: registry.Harness("prime-agent"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	requireNoInstallResult(t, result, label)

	status, err := Inspect(t.Context(), registry.Harness("prime-agent"), testInstallBinary)
	if err != nil {
		t.Fatal(err)
	}
	requireNoInstallStatus(t, status, label)

	removed, err := Remove(t.Context(), Options{Harness: registry.Harness("prime-agent"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	requireNoInstallResult(t, removed, label)

	upgraded, err := Upgrade(t.Context(), testInstallBinary, false)
	if err != nil {
		t.Fatal(err)
	}
	requireUpgradeExcludesHarness(t, upgraded, "prime-agent")
	if _, err := os.Stat(calledPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native catalog command was executed: %v", err)
	}
	requireEmptyDirectory(t, stateDir)
}

func requireNoInstallResult(t *testing.T, result Result, label string) {
	t.Helper()
	want := Result{Harness: "prime-agent", Path: label, Changed: false, Message: "no setup required", NextStep: "", Snippet: "", Error: ""}
	if result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
}

func requireNoInstallStatus(t *testing.T, status IntegrationStatus, label string) {
	t.Helper()
	if status.Status != ArtifactCurrent || status.Message != "no setup required" || len(status.Paths) != 1 || status.Paths[0] != label {
		t.Fatalf("status = %+v", status)
	}
}

func requireUpgradeExcludesHarness(t *testing.T, results []Result, harness string) {
	t.Helper()
	for _, result := range results {
		if result.Harness == harness {
			t.Fatalf("upgrade included no-artifact integration: %+v", result)
		}
	}
}

func requireEmptyDirectory(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("integration management wrote state artifacts: %v", entries)
	}
}

const (
	testInstallBinary     = "/usr/local/bin/aht"
	piExtensionName       = "aht-state.ts"
	ompExtensionName      = "aht-state.ts"
	opencodePluginName    = "aht-state.ts"
	kiloPluginName        = "aht-state.ts"
	ampPluginName         = "aht-state.ts"
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

func TestInstallRejectsBinaryPathThatIsNotUTF8(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")

	_, err := Run(t.Context(), Options{Harness: registry.Harness("pi"), Binary: "/opt/caf\xe9/aht"})
	if !errors.Is(err, errBinaryEncoding) {
		t.Fatalf("install error = %v, want errBinaryEncoding", err)
	}
	entries, readErr := os.ReadDir(home)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("rejected install wrote %v (%v)", entries, readErr)
	}
}

func readTestFile(t *testing.T, path string, description string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", description, err)
	}

	return data
}

func decodeTestJSONObject(t *testing.T, data []byte, description string) map[string]any {
	t.Helper()

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON for %s: %v", description, err)
	}

	return config
}

func requireTextContainsAll(t *testing.T, text string, values []string, description string) {
	t.Helper()

	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("expected %q in %s: %s", value, description, text)
		}
	}
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
