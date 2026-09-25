package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestRuntimeFailureDoesNotPrintUsage(t *testing.T) {
	var stdout bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "info", "missing"}, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected missing session error")
	}
	if strings.Contains(stdout.String(), "USAGE:") || strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("runtime failure printed usage: %q", stdout.String())
	}

	stdout.Reset()
	err = runTestCLI(context.Background(), []string{"info"}, &stdout, &bytes.Buffer{})
	if err == nil {
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
		if err := runTestCLI(context.Background(), test.args, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, test.want) {
			t.Errorf("%v error = %v, want %v", test.args, err, test.want)
		}
	}
}

func TestInfoResolvesShortIDAndRequiresJSONExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	session := observeTestSession(t, store, "info-session", time.Now())
	reference := shortRegistryID(session.ID)

	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "info", reference}, &human, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Session ID:") || !strings.Contains(human.String(), "info-session") || strings.HasPrefix(strings.TrimSpace(human.String()), "{") {
		t.Fatalf("info default output is not human-readable: %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", reference}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil || decoded.ID != session.ID {
		t.Fatalf("info JSON = %q, %v", machine.String(), err)
	}
}

func TestInfoCommandUsesHumanOutputUnlessJSONRequested(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/sessions.json"
	store := registry.NewJournal(path, catalog.Rules{})
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	activity := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: at, Subject: registry.ObservationIdentity{SessionID: "session-1"}, Evidence: &registry.Report{Event: "Stop", Activity: &activity}})
	if err != nil {
		t.Fatal(err)
	}

	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "info", session.ID}, &human, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Session ID:") || strings.HasPrefix(strings.TrimSpace(human.String()), "{") {
		t.Fatalf("expected human session details, got %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", session.ID}, &machine, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var decoded registry.Session
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatalf("expected JSON session: %v; output=%q", err, machine.String())
	}
	if decoded.ID != session.ID {
		t.Fatalf("session id = %q, want %q", decoded.ID, session.ID)
	}
}

func TestInfoExplainReportsFallbackReasonForInactiveIntegration(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/state.json"
	store := registry.NewJournal(path, catalog.Rules{})
	at := time.Now().UTC()
	process := registry.ProcessIdentity{PID: 654, ProcessGroupID: 654, Foreground: true, StartIdentity: "boot:654", Executable: "pi", TTY: "/dev/pts/not-live"}
	presence := registry.PresenceLive
	idle := registry.ActivityIdle
	tmux := registry.Location{Kind: registry.MultiplexerTmux, ServerID: "-L:not-live", SessionID: "$9", SessionName: "agents", WindowID: "@9", WindowIndex: "0", PaneID: "%99", PaneIndex: "0", PanePID: 654, PaneTTY: process.TTY}
	_, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionID: "pi-inactive"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: "old-extension"}, Event: "agent_settled", Claim: &presence, Activity: &idle, Process: &process, Location: &tmux}})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", "--pane", "%99", "--explain"}, &stdout, &bytes.Buffer{}); !errors.Is(err, errTmuxPaneNotLive) {
		t.Fatalf("info explanation missing pane error = %v", err)
	}
	var result explainedInfoResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	explanation := result.Explanation
	if explanation.SelectedAuthority != "screen" || explanation.FallbackReason != "integration_identity_mismatch" || explanation.FinalActivity != "unknown" {
		t.Fatalf("fallback explanation = %#v", explanation)
	}
}

func TestInfoExplainWithoutLivePaneReportsUnavailableState(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/state.json"
	store := registry.NewJournal(path, catalog.Rules{})
	idle := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "no-pane"}, Evidence: &registry.Report{Event: "turn_complete", Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", session.ID, "--explain"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result explainedInfoResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	screen := result.Explanation.Screen
	if screen.Evaluated || screen.UnavailableReason != "no_live_pane" || screen.Error != "" {
		t.Fatalf("screen explanation = %#v", screen)
	}
}

func createActivePiInfoSession(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/state.json"
	store := registry.NewJournal(path, catalog.Rules{})
	at := time.Now().UTC()
	process := registry.ProcessIdentity{PID: 321, ProcessGroupID: 321, Foreground: true, StartIdentity: "boot:321", Executable: "pi", TTY: "/dev/pts/3"}
	presence := registry.PresenceLive
	idle := registry.ActivityIdle
	tmux := registry.Location{Kind: registry.MultiplexerTmux, ServerID: "default", SessionID: "$1", SessionName: "agents", WindowID: "@1", WindowIndex: "1", PaneID: "%3", PaneIndex: "1", PanePID: 10, PaneTTY: "/dev/pts/3"}
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionID: "pi-session"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: "pi-extension"}, Event: "agent_end", Claim: &presence, Activity: &idle, Process: &process, Location: &tmux}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInfoExplainReportsActiveHookAuthorityByPane(t *testing.T) {
	t.Parallel()
	path := createActivePiInfoSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "info", "--pane", "%3", "--explain"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result explainedInfoResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Session.ID == "" || result.Session.SessionID != "pi-session" {
		t.Fatalf("info session = %#v", result.Session)
	}
	explanation := result.Explanation
	if explanation.SelectedAuthority != "hook" || explanation.ProcessMatch != "foreground_tty_process" || explanation.FinalActivity != "idle" {
		t.Fatalf("info explanation = %#v", explanation)
	}
	hook := explanation.Hook
	if !hook.Active || !hook.Fresh || hook.FreshnessReason != "matching_live_process_report" || hook.Integration != "pi-extension" {
		t.Fatalf("hook explanation = %#v", hook)
	}
}

func TestInfoExplainUsesHumanOutputByDefault(t *testing.T) {
	t.Parallel()
	path := createActivePiInfoSession(t)
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "info", "--pane", "%3", "--explain"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Session ID:", "Activity diagnosis:", "Registry activity:", "Effective activity:"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("info explanation omitted %q: %s", expected, stdout.String())
		}
	}
}
