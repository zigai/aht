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
	"time"

	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/internal/service"
	"github.com/zigai/aht/pkg/registry"
)

func TestRootHelpShowsCompactCanonicalSurface(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	for _, command := range []string{"list", "watch", "info", "stop", "manage"} {
		if !strings.Contains(help, command) {
			t.Errorf("root help does not show %q:\n%s", command, help)
		}
	}
	for _, command := range []string{"admin", "setup", "integrations", "monitor", "registry", "doctor", "detection", "detect", "show", "explain", "install-hooks", "observe", "service", "report", "wire", "get", "gc", "queue", "drain", "path", "agy-hook"} {
		if strings.Contains(help, "\n   "+command+" ") || strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("root help exposes internal, nested, or removed command %q:\n%s", command, help)
		}
	}
}

func TestManageHelpShowsCanonicalSurface(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	for _, command := range []string{"setup", "upgrade", "integrations", "tracker", "state", "doctor", "config", "detection"} {
		if !strings.Contains(help, command) {
			t.Errorf("manage help does not show %q:\n%s", command, help)
		}
	}
	for _, command := range []string{"monitor", "registry"} {
		if strings.Contains(help, "\n   "+command+" ") || strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("manage help exposes removed command %q:\n%s", command, help)
		}
	}
}

func TestMachineFacingCommandsAndDestructiveResetAreExplicit(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"hook", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Integration protocol endpoint") {
		t.Fatalf("hook help does not identify the hook protocol endpoint:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "tracker", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Service entry point") {
		t.Fatalf("tracker help does not identify the service entry point:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "state", "reset", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--force") || !strings.Contains(strings.ToLower(stdout.String()), "confirm destructive state reset") {
		t.Fatalf("state reset help omits confirmation requirement:\n%s", stdout.String())
	}
}

func TestEveryHiddenInternalCommandHasCallableHelp(t *testing.T) {
	commands := []string{"report", "hook"}
	for _, command := range commands {
		var stdout bytes.Buffer
		if err := runTestCLI(context.Background(), []string{command, "--help"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Errorf("%s --help failed: %v", command, err)
			continue
		}
		if !strings.Contains(stdout.String(), "USAGE:") && !strings.Contains(stdout.String(), "Usage:") {
			t.Errorf("%s internal help missing usage: %q", command, stdout.String())
		}
	}
}

func TestJSONInvocationFailureLeavesStdoutEmpty(t *testing.T) {
	for _, args := range [][]string{{"--json", "info"}, {"--json", "list", "--not-a-flag"}, {"--json", "unknown"}, {"--json", "report", "--harness", "codex", "--presence", "live"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := executeCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Fatalf("invalid invocation succeeded: %v", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("%v wrote stdout %q", args, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("%v omitted stderr error", args)
		}
	}
}

func TestSubcommandFlagsAreScoped(t *testing.T) {
	tests := [][]string{
		{"manage", "integrations", "remove", "codex", "--force"},
		{"manage", "integrations", "status", "codex", "--dry-run"},
		{"manage", "tracker", "status", "--dry-run"},
		{"manage", "tracker", "disable", "--grace-period", "1s"},
		{"watch", "--summary"},
	}
	for _, args := range tests {
		err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || (!strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "flag provided but not defined")) {
			t.Errorf("%v error = %v, want unknown flag", args, err)
		}
	}
}

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

//nolint:cyclop // one sequential scenario proves both safety and explicit cleanup modes
func TestStateCleanRequiresExplicitPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	presence := registry.PresenceGone
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.HarnessCodex, Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Identity: registry.ObservationIdentity{SessionID: "gone"}, Presence: &presence, ObservedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "clean", "--all", "--older-than", "1h"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unsafe clean error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("unsafe clean changed registry: %v, %#v", err, sessions)
	}
	if err := runTestCLI(context.Background(), []string{"--store", path, "gc"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("legacy gc without an explicit policy unexpectedly succeeded")
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "manage", "state", "clean", "--older-than", "0s"}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var cleanResult registry.GCResult
	if err := json.Unmarshal(machine.Bytes(), &cleanResult); err != nil || cleanResult.Deleted != 1 {
		t.Fatalf("state clean JSON = %q, %v", machine.String(), err)
	}
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.HarnessCodex, Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Identity: registry.ObservationIdentity{SessionID: "gone-again"}, Presence: &presence, ObservedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "clean", "--all", "--yes"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "deleted=1") {
		t.Fatalf("clean output = %q", stdout.String())
	}
}

func TestStatePathAndResetCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	observeTestSession(t, store, "reset-session", time.Now())

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "path"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != path {
		t.Fatalf("state path output = %q", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errStateResetForce) {
		t.Fatalf("state reset without force error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("state reset without force changed state: %v, %#v", err, sessions)
	}

	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset", "--force"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cleared:    1") {
		t.Fatalf("state reset output = %q", stdout.String())
	}
}

func TestStateResetCommandRecoversMalformedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"sessions":`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "manage", "state", "reset", "--force"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cleared:    0") {
		t.Fatalf("state reset output = %q", stdout.String())
	}
	if _, err := registry.NewFileStore(path).List(context.Background(), registry.Filter{}); err != nil {
		t.Fatalf("registry remains unreadable after reset: %v", err)
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

func executeSurfaceCommand(t *testing.T, stdout *bytes.Buffer, args ...string) {
	t.Helper()
	stdout.Reset()
	if err := runTestCLI(context.Background(), args, stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func requireSurfaceOutput(t *testing.T, output string, message string, fragments ...string) {
	t.Helper()
	output = strings.Join(strings.Fields(output), " ")
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("%s: %q", message, output)
		}
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

func TestAgentSelectionSupportsMultipleDeduplicatedAgentsAndAll(t *testing.T) {
	t.Parallel()
	selected, err := selectedHarnesses([]string{"codex", "claude", "codex"}, false)
	if err != nil || len(selected) != 2 || selected[0] != registry.HarnessCodex || selected[1] != registry.HarnessClaude {
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

func TestTrackerLifecycleCommandsUseHumanOutputUnlessJSONRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	storePath := filepath.Join(home, "sessions.json")

	for _, args := range [][]string{{"manage", "tracker", "enable", "--dry-run"}, {"manage", "tracker", "status"}, {"manage", "tracker", "disable", "--dry-run"}} {
		var stdout bytes.Buffer
		if err := runTestCLI(context.Background(), append([]string{"--store", storePath}, args...), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Manager:") {
			t.Fatalf("%v default output = %q", args, stdout.String())
		}
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "enable", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result service.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Manager == "" {
		t.Fatalf("tracker JSON = %q, %v", stdout.String(), err)
	}
}

func TestTrackerRunOnceSupportsHumanAndJSONOutput(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "manage", "tracker", "run", "--once"}, &human, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(human.String()), "{") || !strings.Contains(human.String(), "processes=") {
		t.Fatalf("tracker run human output = %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "run", "--once"}, &machine, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil {
		t.Fatalf("tracker run JSON = %q, %v", machine.String(), err)
	}
}

func TestDoctorIsConciseUnlessVerbose(t *testing.T) {
	assertDoctorSurface(t)
}

func TestDoctorFixtureIgnoresInheritedOMPProfile(t *testing.T) {
	foreignProfile := filepath.Join(t.TempDir(), ".omp", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", foreignProfile)
	installed, err := install.RunContext(t.Context(), install.Options{Harness: registry.HarnessOmp, Binary: defaultInstallBinary()})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}

	path := prepareDoctorEnvironment(t)
	output := executeDoctorSurface(t, "--store", path, "--json", "manage", "doctor")
	var result doctorResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode isolated doctor result: %v\noutput: %s", err, output)
	}
	if !result.OK {
		t.Fatalf("isolated doctor inspected inherited profile:\n%s", output)
	}

	after, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatalf("read foreign OMP integration after doctor: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("doctor fixture modified foreign OMP integration %s", installed.Path)
	}
}

func prepareDoctorEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Like the upgrade fixture, redirect every harness override rather than
	// letting an inherited profile escape the temporary home directory.
	for _, key := range []string{"XDG_CONFIG_HOME", "AHT_CONFIG", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_SHARE_DIR", "GROK_HOME", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "AGY_CONFIG_HOME", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "KILO_CONFIG_DIR", registry.StateDirEnv} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("OMP_PROFILE", "default")
	t.Setenv("PI_PROFILE", "default")
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	return filepath.Join(home, "sessions.json")
}

func assertDoctorSurface(t *testing.T) {
	t.Helper()
	path := prepareDoctorEnvironment(t)
	concise := executeDoctorSurface(t, "--store", path, "manage", "doctor")
	if strings.Contains(concise, "integration.codex") || strings.Contains(concise, "integration.pi") {
		t.Fatalf("concise doctor includes uninstalled integrations:\n%s", concise)
	}
	executeDoctorSurface(t, "manage", "integrations", "install", "codex", "--binary", defaultInstallBinary())
	concise = executeDoctorSurface(t, "--store", path, "manage", "doctor")
	if !strings.Contains(concise, "integration.codex") || strings.Contains(concise, "integration.pi") {
		t.Fatalf("concise doctor omitted installed integration or included uninstalled integrations:\n%s", concise)
	}

	verbose := executeDoctorSurface(t, "--store", path, "manage", "doctor", "--verbose")
	if !strings.Contains(verbose, "integration.pi") || !strings.Contains(verbose, "integration.codex") {
		t.Fatalf("verbose doctor omitted integration details:\n%s", verbose)
	}
	for _, mode := range []struct {
		name         string
		flags        []string
		capabilities bool
	}{
		{name: "concise", flags: nil, capabilities: false},
		{name: "verbose", flags: []string{"--verbose"}, capabilities: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			args := append([]string{"--store", path, "--json", "manage", "doctor"}, mode.flags...)
			output := executeDoctorSurface(t, args...)
			var result doctorResult
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("decode doctor JSON: %v\nstdout:\n%s", err, output)
			}
			if !result.OK || (len(result.Capabilities) != 0) != mode.capabilities {
				t.Fatalf("doctor health or capability visibility mismatch:\n%s", output)
			}
		})
	}
}

func executeDoctorSurface(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("aht %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func observeTestSession(t *testing.T, store registry.Store, sessionID string, at time.Time) registry.Session {
	t.Helper()
	activity := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.HarnessCodex, Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Identity: registry.ObservationIdentity{SessionID: sessionID}, Activity: &activity, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return session
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
