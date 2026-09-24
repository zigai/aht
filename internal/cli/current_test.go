package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
)

func TestCurrentFailsWhenNoAmbientAgent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", path, "current"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when running aht current with no ambient agent")
	}
	if !strings.Contains(err.Error(), "no current agent session") {
		t.Fatalf("expected 'no current agent session' in error, got: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty on error, got: %q", stdout.String())
	}

	// Also test with --json
	stdout.Reset()
	stderr.Reset()
	err = runTestCLI(context.Background(), []string{"--store", path, "--json", "current"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when running aht --json current with no ambient agent")
	}
	if !strings.Contains(err.Error(), "no current agent session") {
		t.Fatalf("expected 'no current agent session' in error, got: %v", err)
	}
}

func TestListProjectAndCWDAndLocationFilterFlags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})

	tempDir := t.TempDir()
	proj1 := filepath.Join(tempDir, "proj1")
	proj2 := filepath.Join(tempDir, "proj2")
	cwdSub := filepath.Join(proj1, "subdir")

	live := registry.PresenceLive
	activity := registry.ActivityRunning

	// Session 1: proj1, subdir, tmux /tmp/s1.sock %1
	s1, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-1"}, Evidence: &registry.Report{Claim: &live, Activity: &activity, Location: &registry.Location{
		Kind:            registry.MultiplexerTmux,
		ServerID:        "/tmp/s1.sock",
		SessionID:       "$1",
		SessionName:     "sess1",
		PaneID:          "%1",
		PaneCurrentPath: cwdSub,
	}, Listing: &registry.Listing{
		CWD:         cwdSub,
		ProjectRoot: proj1,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	// Session 2: root proj2, working dir proj2, zellij %1
	s2, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-2"}, Evidence: &registry.Report{Claim: &live, Activity: &activity, Location: &registry.Location{
		Kind:            registry.MultiplexerZellij,
		ServerID:        "",
		SessionID:       "z1",
		SessionName:     "main",
		PaneID:          "%1",
		PaneCurrentPath: proj2,
	}, Listing: &registry.Listing{
		CWD:         proj2,
		ProjectRoot: proj2,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	assertListMatchingSessions(t, path, []string{"--project", proj1}, []string{s1.ID})
	assertListMatchingSessions(t, path, []string{"--project", tempDir, "--project-subtree"}, []string{s1.ID, s2.ID})
	assertListMatchingSessions(t, path, []string{"--cwd", cwdSub}, []string{s1.ID})
	assertListMatchingSessions(t, path, []string{"--multiplexer", "zellij"}, []string{s2.ID})
	assertListMatchingSessions(t, path, []string{"--pane", "%1", "--server", "/tmp/s1.sock"}, []string{s1.ID})
}

func assertListMatchingSessions(t *testing.T, path string, filterArgs []string, wantIDs []string) {
	t.Helper()
	args := append([]string{"--store", path, "--json", "list"}, filterArgs...)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), args, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("runTestCLI %v failed: %v", args, err)
	}
	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(sessions) != len(wantIDs) {
		t.Fatalf("list %v returned %d sessions, want %d", filterArgs, len(sessions), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if sessions[i].ID != wantID {
			t.Fatalf("session[%d] = %q, want %q", i, sessions[i].ID, wantID)
		}
	}
}

func TestInfoQualifiedPaneResolution(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})

	live := registry.PresenceLive
	activity := registry.ActivityRunning

	// Two sessions in pane %0 on two different servers
	s1, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "s1"}, Evidence: &registry.Report{Claim: &live, Activity: &activity, Location: &registry.Location{
		Kind:     registry.MultiplexerTmux,
		ServerID: "/tmp/server1.sock",
		PaneID:   "%0",
	}}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "s2"}, Evidence: &registry.Report{Claim: &live, Activity: &activity, Location: &registry.Location{
		Kind:     registry.MultiplexerTmux,
		ServerID: "/tmp/server2.sock",
		PaneID:   "%0",
	}}})
	if err != nil {
		t.Fatal(err)
	}

	// Bare info --pane %0 is ambiguous
	var stdout, stderr bytes.Buffer
	err = runTestCLI(context.Background(), []string{"--store", path, "info", "--pane", "%0"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for ambiguous bare --pane %0")
	}

	// Qualified info --pane %0 --server /tmp/server1.sock resolves s1
	stdout.Reset()
	stderr.Reset()
	err = runTestCLI(context.Background(), []string{"--store", path, "--json", "info", "--pane", "%0", "--server", "/tmp/server1.sock"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("qualified info --pane failed: %v", err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded.ID != s1.ID {
		t.Fatalf("qualified info resolved %q, want %q", decoded.ID, s1.ID)
	}
}

func TestCurrentWithVerifiedProcess(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})

	live := registry.PresenceLive
	activity := registry.ActivityRunning

	proc, found, err := processinfo.Find(t.Context(), os.Getpid())
	if err != nil || !found {
		t.Fatalf("find current process: found=%v err=%v", found, err)
	}

	s, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "zellij-sess-current"}, Evidence: &registry.Report{Claim: &live, Activity: &activity, Process: &registry.ProcessIdentity{PID: proc.PID, StartIdentity: proc.StartIdentity}, Location: &registry.Location{
		Kind:        registry.MultiplexerZellij,
		SessionName: "cli-test-session",
		PaneID:      "terminal_55",
	}}})
	if err != nil {
		t.Fatal(err)
	}

	// Test human-readable output
	var human, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "current"}, &human, &stderr); err != nil {
		t.Fatalf("current command failed: %v, stderr: %q", err, stderr.String())
	}
	if !strings.Contains(human.String(), "Session ID:") || !strings.Contains(human.String(), s.ID) {
		t.Fatalf("human-readable current output missing session details: %q", human.String())
	}

	// Test JSON output
	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "current"}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatalf("current --json failed: %v", err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil || decoded.ID != s.ID {
		t.Fatalf("json unmarshal failed: %v, decoded: %#v", err, decoded)
	}
}
