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
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	previousPosition := -1
	for _, command := range []string{"list", "watch", "info", "stop", "manage", "hook", "help"} {
		position := strings.Index(help, "\n  "+command+" ")
		if position < 0 {
			t.Errorf("root help does not show %q:\n%s", command, help)
			continue
		}
		if position <= previousPosition {
			t.Errorf("root help shows %q out of order:\n%s", command, help)
		}
		previousPosition = position
	}
	for _, command := range []string{"admin", "setup", "integrations", "monitor", "registry", "doctor", "detection", "detect", "show", "explain", "install-hooks", "observe", "service", "report", "get", "gc", "queue", "drain", "path", "agy-hook"} {
		if strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("root help exposes internal, nested, or removed command %q:\n%s", command, help)
		}
	}
	for _, title := range []string{"Sessions:", "Setup:", "System:", "Everyday Commands:", "Configuration Commands:"} {
		if strings.Contains(help, title) {
			t.Errorf("root help still shows command group %q:\n%s", title, help)
		}
	}
}

func TestManageHelpShowsCanonicalSurface(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	previousPosition := -1
	for _, command := range []string{"setup", "integrations", "tracker", "state", "doctor"} {
		position := strings.Index(help, "\n  "+command+" ")
		if position < 0 {
			t.Errorf("manage help does not show %q:\n%s", command, help)
			continue
		}
		if position <= previousPosition {
			t.Errorf("manage help shows %q out of order:\n%s", command, help)
		}
		previousPosition = position
	}
	for _, command := range []string{"monitor", "registry", "detection"} {
		if strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("manage help exposes removed command %q:\n%s", command, help)
		}
	}
}

func TestMachineFacingCommandsAndDestructiveResetAreExplicit(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "hook        Integration protocol endpoint; not intended for manual use") {
		t.Fatalf("root help does not identify the hook protocol endpoint:\n%s", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "tracker", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "run         Service entry point; not intended for manual use") {
		t.Fatalf("tracker help does not identify the service entry point:\n%s", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "state", "reset", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--force") || !strings.Contains(stdout.String(), "confirm destructive state reset") {
		t.Fatalf("state reset help omits confirmation requirement:\n%s", stdout.String())
	}
}

func TestEveryHiddenInternalCommandHasCallableHelp(t *testing.T) {
	commands := []string{"report"}
	for _, command := range commands {
		var stdout bytes.Buffer
		root := NewRootCommand(&stdout, &bytes.Buffer{})
		root.SetArgs([]string{command, "--help"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Errorf("%s --help failed: %v", command, err)
			continue
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Errorf("%s internal help missing usage: %q", command, stdout.String())
		}
	}
}

func TestRuntimeFailureDoesNotPrintUsage(t *testing.T) {
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "info", "missing"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected missing session error")
	}
	if strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("runtime failure printed usage: %q", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"info"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected invocation error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("invocation error wrote stdout: %q", stdout.String())
	}
}

func TestInfoValidatesReferenceAndExplanationFlags(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		args []string
		want error
	}{
		{args: []string{"info"}, want: errInfoReference},
		{args: []string{"info", "session", "--pane", "%1"}, want: errInfoReference},
		{args: []string{"info", "session", "--config-dir", t.TempDir()}, want: errInfoConfig},
	} {
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(test.args)
		if err := root.ExecuteContext(context.Background()); !errors.Is(err, test.want) {
			t.Errorf("%v error = %v, want %v", test.args, err, test.want)
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

func TestListRejectsModeSpecificFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	tests := [][]string{
		{"--store", path, "list", "--format", "plain"},
		{"--store", path, "list", "--no-snapshot"},
		{"--store", path, "list", "--watch", "--summary"},
		{"--store", path, "list", "--watch", "--summary=false"},
		{"--store", path, "list", "--watch", "--sort", "updated"},
		{"--store", path, "list", "--summary", "--desc"},
		{"--store", path, "list", "--summary", "--absolute-time"},
		{"--store", path, "--json", "list", "--absolute-time"},
		{"--store", path, "list", "--sort", ""},
		{"--store", path, "watch", "--format", ""},
	}
	for _, args := range tests {
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Errorf("arguments unexpectedly accepted: %v", args)
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
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v error = %v, want unknown flag", args, err)
		}
	}
}

func TestIntegrationsInstallRejectsTargetBinaryWithoutShim(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"manage", "integrations", "install", "codex", "--target-binary", "/bin/codex"}} {
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); !errors.Is(err, errTargetBinaryNeedsShim) {
			t.Errorf("%v target binary error = %v", args, err)
		}
	}
	for _, args := range [][]string{{"manage", "integrations", "install", "all", "--shim", "--target-binary", "/bin/agent"}} {
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); !errors.Is(err, errTargetBinaryWithAll) {
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

	root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "clean"})
	if err := root.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("unsafe clean error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("unsafe clean changed registry: %v, %#v", err, sessions)
	}
	root = NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "gc"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("legacy gc without an explicit policy unexpectedly succeeded")
	}

	var machine bytes.Buffer
	root = NewRootCommand(&machine, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "--json", "manage", "state", "clean", "--older-than", "0s"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "clean", "--all", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "path"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != path {
		t.Fatalf("state path output = %q", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "reset"})
	if err := root.ExecuteContext(context.Background()); !errors.Is(err, errStateResetForce) {
		t.Fatalf("state reset without force error = %v", err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("state reset without force changed state: %v, %#v", err, sessions)
	}

	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "reset", "--force"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "state", "reset", "--force"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cleared:    0") {
		t.Fatalf("state reset output = %q", stdout.String())
	}
	if _, err := registry.NewFileStore(path).List(context.Background(), registry.Filter{}); err != nil {
		t.Fatalf("registry remains unreadable after reset: %v", err)
	}
}

func TestListDefaultsToLatestUpdateLastWithUsefulLabelsAndShortIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	old := observeTestSession(t, store, "older-session", time.Now().Add(-time.Hour))
	newer := observeTestSession(t, store, "newer-session", time.Now())

	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "list"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Index(output, "older-session") > strings.Index(output, "newer-session") {
		t.Fatalf("list does not put the latest update last:\n%s", output)
	}
	if !strings.Contains(output, "Session") || strings.Contains(output, old.ID) || strings.Contains(output, newer.ID) {
		t.Fatalf("list did not use a label and abbreviated IDs:\n%s", output)
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "--json", "list"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil || len(sessions) != 2 || sessions[0].ID != old.ID || sessions[1].ID != newer.ID {
		t.Fatalf("list JSON = %q, %v", stdout.String(), err)
	}
}

func TestListDisplaysAndFiltersZellijLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	location := &registry.MultiplexerContext{
		Kind: registry.MultiplexerZellij, SessionName: "work", TabName: "agents", PaneID: "terminal_7",
	}
	if _, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "zellij-session"},
		NativeEvent: "turn_complete", Multiplexer: location, ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "list", "--multiplexer-session", "work"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, expected := range []string{"Location", "zellij", "work"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("list output missing %q:\n%s", expected, output)
		}
	}
}

func TestAbbreviatedRegistryIDsExpandCollidingPrefixes(t *testing.T) {
	t.Parallel()
	sessions := []registry.Session{{ID: "codex-12345678aaaa"}, {ID: "codex-12345678bbbb"}, {ID: "claude-12345678cccc"}}
	ids := abbreviatedRegistryIDs(sessions)
	if ids[sessions[0].ID] != "codex-12345678a" || ids[sessions[1].ID] != "codex-12345678b" {
		t.Fatalf("colliding IDs were not expanded: %#v", ids)
	}
	if ids[sessions[2].ID] != "claude-12345678" {
		t.Fatalf("different agent prefix was unnecessarily expanded: %#v", ids)
	}
}

func TestInfoResolvesShortIDAndRequiresJSONExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	session := observeTestSession(t, store, "info-session", time.Now())
	reference := shortRegistryID(session.ID)

	var human bytes.Buffer
	root := NewRootCommand(&human, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "info", reference})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Session ID:") || !strings.Contains(human.String(), "info-session") || strings.HasPrefix(strings.TrimSpace(human.String()), "{") {
		t.Fatalf("info default output is not human-readable: %q", human.String())
	}

	var machine bytes.Buffer
	root = NewRootCommand(&machine, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "--json", "info", reference})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil || decoded.ID != session.ID {
		t.Fatalf("info JSON = %q, %v", machine.String(), err)
	}
}

func createSkippedStopSession(t *testing.T) (string, registry.Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	present := true
	session, err := store.Observe(context.Background(), registry.Observation{
		Harness: registry.HarnessCodex, Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Identity: registry.ObservationIdentity{SessionID: "stop-session"}, ProcessPresent: &present,
		Process: &registry.ProcessIdentity{PID: 1_000_000_000, StartIdentity: "missing:1000000000"}, ObservedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, session
}

func TestStopExplicitSkippedTargetReturnsReasonAndError(t *testing.T) {
	path, session := createSkippedStopSession(t)
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "stop", shortRegistryID(session.ID), "--dry-run"})
	if err := root.ExecuteContext(context.Background()); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("single skipped stop error = %v", err)
	}
	if !strings.Contains(stdout.String(), "process no longer exists") || !strings.Contains(stdout.String(), "skipped=1") {
		t.Fatalf("single stop output = %q", stdout.String())
	}
}

func TestStopAllKeepsSkippedResultsMachineReadable(t *testing.T) {
	path, _ := createSkippedStopSession(t)
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "--json", "stop", "--all", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["stoppable"] != float64(0) || result["skipped"] != float64(1) || result["dry_run"] != true {
		t.Fatalf("stop JSON = %q, %v", stdout.String(), err)
	}
	if _, exists := result["Stoppable"]; exists {
		t.Fatalf("stop JSON retained exported Go field casing: %q", stdout.String())
	}
}

func TestStopRejectsInvalidSelection(t *testing.T) {
	path, session := createSkippedStopSession(t)
	for _, args := range [][]string{{"stop"}, {"stop", shortRegistryID(session.ID), "--all"}} {
		root := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
		root.SetArgs(append([]string{"--store", path}, args...))
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Fatalf("invalid stop selection accepted: %v", args)
		}
	}
}

func TestStopMultipleSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	now := time.Now().UTC()
	live := registry.PresenceLive
	s1, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "session-1"},
		Presence: &live, NativeEvent: "start", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessClaude, Identity: registry.ObservationIdentity{SessionID: "session-2"},
		Presence: &live, NativeEvent: "start", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "stop", shortRegistryID(s1.ID), shortRegistryID(s2.ID), "--dry-run"})
	if err := root.ExecuteContext(context.Background()); !errors.Is(err, errStopTargetSkipped) {
		t.Fatalf("expected skipped error, got %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "skipped=2") || !strings.Contains(output, "no stop target") {
		t.Fatalf("stop output missing both session results:\n%s", output)
	}
}

func TestStopAllConfirmationHandling(t *testing.T) {
	path, _ := createSkippedStopSession(t)

	// Refused confirmation via stdin
	var stdout, stderr bytes.Buffer
	root := NewRootCommand(&stdout, &stderr)
	root.SetIn(strings.NewReader("n\n"))
	root.SetArgs([]string{"--store", path, "stop", "--all"})
	if err := root.ExecuteContext(context.Background()); !errors.Is(err, errStopAllConfirmation) {
		t.Fatalf("expected confirmation cancellation error, got %v", err)
	}
	if !strings.Contains(stderr.String(), "Stop all live sessions? [y/N]: ") || strings.Contains(stdout.String(), "Stop all live sessions?") {
		t.Fatalf("confirmation prompt channel mismatch: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// Accepted confirmation via stdin
	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetIn(strings.NewReader("y\n"))
	root.SetArgs([]string{"--store", path, "stop", "--all"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("expected successful confirmation, got %v", err)
	}

	// Accepted via -y flag
	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "stop", "--all", "-y"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("expected successful stop with -y, got %v", err)
	}

	// Accepted via --yes flag
	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "stop", "--all", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("expected successful stop with --yes, got %v", err)
	}
}

//nolint:cyclop // the round trip intentionally verifies each observable state in order
func TestIntegrationsInstallStatusRemoveRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))

	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "install", "claude", "--binary", "/bin/aht"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Agent") || !strings.Contains(stdout.String(), "claude") {
		t.Fatalf("install output = %q", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "status", "claude", "--binary", "/bin/aht"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "current") {
		t.Fatalf("status output = %q", stdout.String())
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--json", "manage", "integrations", "status", "claude", "--binary", "/bin/aht"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var statuses []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil || len(statuses) != 1 || statuses[0]["status"] != "current" {
		t.Fatalf("integration status JSON = %q, %v", stdout.String(), err)
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "remove", "claude"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Fatalf("remove output = %q", stdout.String())
	}
	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "status", "claude"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&concise, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(concise.String(), `"hooks":`) || !strings.Contains(concise.String(), "dry run") {
		t.Fatalf("concise install output = %q", concise.String())
	}

	var detailed bytes.Buffer
	root = NewRootCommand(&detailed, &bytes.Buffer{})
	root.SetArgs([]string{"manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run", "--show-content"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detailed.String(), "codex generated content:") || !strings.Contains(detailed.String(), `"hooks":`) {
		t.Fatalf("detailed install output omitted generated content: %q", detailed.String())
	}

	var machine bytes.Buffer
	root = NewRootCommand(&machine, &bytes.Buffer{})
	root.SetArgs([]string{"--json", "manage", "integrations", "install", "codex", "--binary", "/bin/aht", "--dry-run", "--show-content"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(stdout, &bytes.Buffer{})
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", filepath.Join(home, "sessions.json"), "manage", "setup", "codex", "--binary", "/bin/aht", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "codex") || !strings.Contains(stdout.String(), "tracker:") || strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("setup output = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("setup dry run wrote integration: %v", err)
	}

	stdout.Reset()
	root = NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", filepath.Join(home, "sessions.json"), "--json", "manage", "setup", "codex", "--binary", "/bin/aht", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", filepath.Join(home, "sessions.json"), "manage", "setup", "openclaw", "--binary", "/bin/aht"})
	err := root.ExecuteContext(context.Background())
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
		root := NewRootCommand(&stdout, &bytes.Buffer{})
		root.SetArgs(append([]string{"--store", storePath}, args...))
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Manager:") {
			t.Fatalf("%v default output = %q", args, stdout.String())
		}
	}

	var stdout bytes.Buffer
	root := NewRootCommand(&stdout, &bytes.Buffer{})
	root.SetArgs([]string{"--store", storePath, "--json", "manage", "tracker", "enable", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
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
	root := NewRootCommand(&human, &bytes.Buffer{})
	root.SetArgs([]string{"--store", storePath, "manage", "tracker", "run", "--once"})
	if err := root.ExecuteContext(context.Background()); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(human.String()), "{") || !strings.Contains(human.String(), "processes=") {
		t.Fatalf("tracker run human output = %q", human.String())
	}

	var machine bytes.Buffer
	root = NewRootCommand(&machine, &bytes.Buffer{})
	root.SetArgs([]string{"--store", storePath, "--json", "manage", "tracker", "run", "--once"})
	if err := root.ExecuteContext(context.Background()); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil {
		t.Fatalf("tracker run JSON = %q, %v", machine.String(), err)
	}
}

func TestDoctorIsConciseUnlessVerbose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	path := filepath.Join(home, "sessions.json")

	normalize := func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	}

	var concise bytes.Buffer
	root := NewRootCommand(&concise, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "doctor"})
	_ = root.ExecuteContext(context.Background())
	if strings.Contains(normalize(concise.String()), "Run/Idle") || strings.Contains(concise.String(), "integration.codex") {
		t.Fatalf("concise doctor contains full matrix:\n%s", concise.String())
	}
	installRoot := NewRootCommand(&bytes.Buffer{}, &bytes.Buffer{})
	installRoot.SetArgs([]string{"manage", "integrations", "install", "codex", "--binary", defaultInstallBinary()})
	if err := installRoot.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	concise.Reset()
	root = NewRootCommand(&concise, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "doctor"})
	_ = root.ExecuteContext(context.Background())
	if !strings.Contains(concise.String(), "integration.codex") || strings.Contains(normalize(concise.String()), "Run/Idle") {
		t.Fatalf("concise doctor omitted installed integration or added matrix:\n%s", concise.String())
	}

	var verbose bytes.Buffer
	root = NewRootCommand(&verbose, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "manage", "doctor", "--verbose"})
	_ = root.ExecuteContext(context.Background())
	if !strings.Contains(normalize(verbose.String()), "Run/Idle") || !strings.Contains(verbose.String(), "Start") || !strings.Contains(verbose.String(), "integration.codex") {
		t.Fatalf("verbose doctor omitted details:\n%s", verbose.String())
	}

	var machine bytes.Buffer
	root = NewRootCommand(&machine, &bytes.Buffer{})
	root.SetArgs([]string{"--store", path, "--json", "manage", "doctor"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var result doctorResult
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil || len(result.Capabilities) != 0 {
		t.Fatalf("concise doctor JSON = %q, %v", machine.String(), err)
	}
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
