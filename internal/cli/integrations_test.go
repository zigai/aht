package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestIntegrationsInstallRejectsTargetBinaryWithoutShim(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"manage", "integrations", "install", "codex", "--target-binary", "/bin/codex"}} {
		if err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, errTargetBinaryNeedsShim) {
			t.Errorf("%v target binary error = %v", args, err)
		}
	}
	for _, args := range [][]string{{"manage", "integrations", "install", "all", "--shim", "--target-binary", "/bin/agent"}} {
		if err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, errTargetBinaryWithAll) {
			t.Errorf("%v all target binary error = %v", args, err)
		}
	}
}

//nolint:cyclop // the round trip intentionally verifies each observable state in order
func TestIntegrationsInstallStatusRemoveRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "install", "claude", "--binary", "/bin/aht"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Agent") || !strings.Contains(stdout.String(), "claude") {
		t.Fatalf("install output = %q", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "status", "claude", "--binary", "/bin/aht"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "current") {
		t.Fatalf("status output = %q", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--json", "manage", "integrations", "status", "claude", "--binary", "/bin/aht"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var statuses []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil || len(statuses) != 1 || statuses[0]["status"] != "current" {
		t.Fatalf("integration status JSON = %q, %v", stdout.String(), err)
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "remove", "claude"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Fatalf("remove output = %q", stdout.String())
	}
	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "status", "claude"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "missing") {
		t.Fatalf("removed integration status = %q", stdout.String())
	}
}

func TestCodexInstallAndStatusSurfaceHookTrustStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))

	var stdout bytes.Buffer
	executeSurfaceCommand(t, &stdout, "manage", "integrations", "install", "codex", "--binary", "/bin/aht-v1")
	requireSurfaceOutput(t, stdout.String(), "Codex install omitted trust activation", "next:", "/hooks")

	executeSurfaceCommand(t, &stdout, "manage", "integrations", "status", "codex", "--binary", "/bin/aht-v1")
	requireSurfaceOutput(t, stdout.String(), "Codex status omitted trust verification", "current", "/hooks", "trust status")

	executeSurfaceCommand(t, &stdout, "--json", "manage", "integrations", "install", "codex", "--binary", "/bin/aht-v2")
	requireCodexUpdateTrustJSON(t, stdout.Bytes())
}

func TestIntegrationsInstallShowsGeneratedContentOnlyWhenRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))

	var concise bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run"}, &concise, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(concise.String(), `"hooks":`) || !strings.Contains(concise.String(), "dry run") {
		t.Fatalf("concise install output = %q", concise.String())
	}

	var detailed bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run", "--show-content"}, &detailed, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detailed.String(), "codex generated content:") || !strings.Contains(detailed.String(), `"hooks":`) {
		t.Fatalf("detailed install output omitted generated content: %q", detailed.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--json", "manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run", "--show-content"}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var results []map[string]any
	if err := json.Unmarshal(machine.Bytes(), &results); err != nil || len(results) != 1 {
		t.Fatalf("install JSON is not an array: %q, %v", machine.String(), err)
	}
}

func requireCodexUpdateTrustJSON(t *testing.T, data []byte) {
	t.Helper()
	var results []map[string]any
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("Codex update JSON = %q, %v", data, err)
	}
	if len(results) != 1 {
		t.Fatalf("Codex update JSON = %q, want one result", data)
	}
	nextStep, ok := results[0]["next_step"].(string)
	if !ok || results[0]["changed"] != true || !strings.Contains(nextStep, "/hooks") {
		t.Fatalf("Codex update omitted trust activation: %#v", results[0])
	}
}

func TestAgentSelectionSupportsMultipleDeduplicatedAgentsAndAll(t *testing.T) {
	t.Parallel()
	selected, err := selectedHarnesses([]string{"codex", "claude", "codex"}, false)
	if err != nil || len(selected) != 2 || selected[0] != registry.Harness("codex") || selected[1] != registry.Harness("claude") {
		t.Fatalf("selected agents = %v, %v", selected, err)
	}
	selected, err = selectedHarnesses([]string{"all"}, false)
	if err != nil || len(selected) == 0 {
		t.Fatalf("all agents = %v, %v", selected, err)
	}
	if _, err := selectedHarnesses([]string{"all", "codex"}, false); !errors.Is(err, errAllWithAgents) {
		t.Fatalf("mixed all selection error = %v", err)
	}
}

func TestSetupDryRunCombinesIntegrationAndTracker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", filepath.Join(home, "sessions.json"), "manage", "setup", "codex", "--binary", "/bin/aht", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "codex") || !strings.Contains(stdout.String(), "tracker:") || strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("setup output = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("setup dry run wrote integration: %v", err)
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", filepath.Join(home, "sessions.json"), "--json", "manage", "setup", "codex", "--binary", "/bin/aht", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result setupResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Integrations) != 1 || result.Tracker.Manager == "" {
		t.Fatalf("setup JSON = %q, %v", stdout.String(), err)
	}
}

func TestSetupEnablesTrackerWhenIntegrationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	binDir := t.TempDir()
	for _, executable := range []string{"systemctl", "launchctl"} {
		path := filepath.Join(binDir, executable)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
			t.Fatalf("writing fake %s: %v", executable, err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatalf("making fake %s executable: %v", executable, err)
		}
	}
	t.Setenv("PATH", binDir)

	var stdout bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", filepath.Join(home, "sessions.json"), "manage", "setup", "openclaw", "--binary", "/bin/aht"}, &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "OpenClaw CLI is required") {
		t.Fatalf("setup error = %v, want missing OpenClaw CLI", err)
	}
	if !strings.Contains(stdout.String(), "tracker: installed") {
		t.Fatalf("setup did not continue to tracker after integration failure: %q", stdout.String())
	}
}

func TestIntegrationResultTableColumnsAdaptsToContentAndTerminalWidth(t *testing.T) {
	rows := [][]string{
		{"codex", "false", "/home/user/.codex/hooks.json", "codex hooks already installed"},
		{"grok", "false", "/home/user/.grok/hooks/aht-state.json", "grok hooks already installed"},
		{"openclaw", "false", "/home/user/.local/state/aht/integrations/openclaw/aht-state", "OpenClaw plugin already installed; next: restart the harness to load updated plugin code; registration and permissions preserved"},
	}

	// In standard 120-column terminal, Path must be allocated enough space to fit all paths without truncation/wrapping.
	cols120 := integrationResultTableColumns(rows, 120)
	longestPath := len("/home/user/.local/state/aht/integrations/openclaw/aht-state")
	if cols120[2].width < longestPath {
		t.Fatalf("Path column width in 120-column terminal = %d, want >= %d", cols120[2].width, longestPath)
	}
	if cols120[2].wrap == nil {
		t.Fatal("Path column wrap function should be set to wrapHumanPath")
	}

	// In 200-column terminal, Path fits completely and Result gets all remaining space (120 cols).
	cols200 := integrationResultTableColumns(rows, 200)
	if cols200[2].width < longestPath {
		t.Fatalf("Path column width in 200-column terminal = %d, want >= %d", cols200[2].width, longestPath)
	}
	if cols200[3].width < 120 {
		t.Fatalf("Result column width in 200-column terminal = %d, want >= 120", cols200[3].width)
	}

	// In 220-column terminal, both Path and Result columns fit completely without any wrapping.
	cols220 := integrationResultTableColumns(rows, 220)
	if cols220[2].width < longestPath {
		t.Fatalf("Path column width in 220-column terminal = %d, want >= %d", cols220[2].width, longestPath)
	}
	longestResult := len("OpenClaw plugin already installed; next: restart the harness to load updated plugin code; registration and permissions preserved")
	if cols220[3].width < longestResult {
		t.Fatalf("Result column width in 220-column terminal = %d, want >= %d", cols220[3].width, longestResult)
	}
	colsNarrow := integrationResultTableColumns(rows, 60)
	if colsNarrow[2].width < 4 {
		t.Fatalf("Path column width in narrow terminal = %d, want >= 4", colsNarrow[2].width)
	}
	if colsNarrow[3].width < 6 {
		t.Fatalf("Result column width in narrow terminal = %d, want >= 6", colsNarrow[3].width)
	}
}

func TestWriteIntegrationResultsRendersFullPathsWithoutWrapping(t *testing.T) {
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	results := []install.Result{
		{Harness: "grok", Changed: false, Path: "/home/user/.grok/hooks/aht-state.json", Message: "grok hooks already installed"},
		{Harness: "openclaw", Changed: false, Path: "/home/user/.local/state/aht/integrations/openclaw/aht-state", Message: "OpenClaw plugin already installed"},
	}
	if err := app.writeIntegrationResults(results, false); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "/home/user/.grok/hooks/aht-state.json") {
		t.Fatalf("output wrapped or truncated grok path:\n%s", output)
	}
	if !strings.Contains(output, "/home/user/.local/state/aht/integrations/openclaw/aht-state") {
		t.Fatalf("output wrapped or truncated openclaw path:\n%s", output)
	}
	if strings.Contains(output, "restart the harness") {
		t.Fatalf("output unexpectedly contains restart instructions for unchanged plugin:\n%s", output)
	}
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "on" || trimmed == "e" || trimmed == "-state.ts" {
			t.Fatalf("found fragmented path line %q in output:\n%s", trimmed, output)
		}
	}
}
