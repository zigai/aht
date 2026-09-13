package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestSameTmuxServerDoesNotTreatMissingIdentityAsWildcard(t *testing.T) {
	t.Parallel()
	if !sameTmuxServer("", "default") || sameTmuxServer("-L:work", "") || sameTmuxServer("-L:work", "-L:other") {
		t.Fatal("tmux server matching was not conservative")
	}
}

func TestInfoExplainReportsFallbackReasonForInactiveIntegration(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/state.json"
	store := registry.NewFileStore(path)
	at := time.Now().UTC()
	process := registry.ProcessIdentity{PID: 654, ProcessGroupID: 654, Foreground: true, StartIdentity: "boot:654", Executable: "pi", TTY: "/dev/pts/not-live"}
	presence := registry.PresenceLive
	idle := registry.ActivityIdle
	tmux := registry.TmuxContext{Inside: true, ServerSocket: "-L:not-live", SessionID: "$9", SessionName: "agents", WindowID: "@9", WindowIndex: "0", PaneID: "%99", PaneIndex: "0", PanePID: 654, PaneTTY: process.TTY}
	_, err := store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionID: "pi-inactive"}, Presence: &presence, Activity: &idle, NativeEvent: "agent_settled", Process: &process, Tmux: &tmux, Attributes: map[string]string{"aht_integration": "old-extension"}, ObservedAt: at})
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
	store := registry.NewFileStore(path)
	idle := registry.ActivityIdle
	activityAuthoritative := false
	session, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "no-pane"},
		NativeEvent: "turn_complete", Activity: &idle, ActivityAuthoritative: &activityAuthoritative, ObservedAt: time.Now().UTC(),
	})
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
	store := registry.NewFileStore(path)
	at := time.Now().UTC()
	process := registry.ProcessIdentity{PID: 321, ProcessGroupID: 321, Foreground: true, StartIdentity: "boot:321", Executable: "pi", TTY: "/dev/pts/3"}
	presence := registry.PresenceLive
	idle := registry.ActivityIdle
	tmux := registry.TmuxContext{Inside: true, ServerSocket: "default", SessionID: "$1", SessionName: "agents", WindowID: "@1", WindowIndex: "1", PaneID: "%3", PaneIndex: "1", PanePID: 10, PaneTTY: "/dev/pts/3"}
	if _, err := store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionID: "pi-session"}, Presence: &presence, Activity: &idle, NativeEvent: "agent_end", Process: &process, Tmux: &tmux, Attributes: map[string]string{"aht_integration": "pi-extension"}, ObservedAt: at}); err != nil {
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
