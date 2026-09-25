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

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
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
	if !slices.Contains(harnesses, registry.Harness("codex")) {
		t.Fatalf("AllHarnesses() = %v, want codex", harnesses)
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

func requireTextContainsAll(t *testing.T, text string, values []string, context string) {
	t.Helper()

	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Fatalf("expected %q in %s: %s", value, context, text)
		}
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
