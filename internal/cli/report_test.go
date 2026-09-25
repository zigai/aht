package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestReportQuietSuppressesHumanOutput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", t.TempDir() + "/sessions.json", "report", "codex", "--session-id", "json", "--event", "start", "--quiet"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("quiet report wrote output: %q", stdout.String())
	}
}

func TestReportCommandDefaultsToHumanOutput(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "report", "codex", "--session-id", "human", "--event", "start", "--no-tmux"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "codex") {
		t.Fatalf("report default output = %q", stdout.String())
	}
}

func TestReportHumanOutputDistinguishesReportedAndEffectiveActivity(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "report", "codex", "--session-id", "activity", "--event", "turn_complete", "--activity", "waiting", "--no-tmux"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, expected := range []string{"Reported", "Effective", "Authoritative", "waiting", "unknown", "no"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("report output omitted %q: %s", expected, output)
		}
	}
}

func TestReportCommandEmitsJSONOnlyWhenRequested(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", filepath.Join(t.TempDir(), "sessions.json"), "--json", "report", "codex", "--session-id", "machine", "--event", "start", "--no-tmux"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var session registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil || session.SchemaVersion != expectedSessionSchemaVersion {
		t.Fatalf("report JSON = %q, %v", stdout.String(), err)
	}
}

func TestReportJSONCoversIgnoredResult(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"codex-session","transcript_path":"/home/user/.codex/sessions/rollout.jsonl","hook_event_name":"Stop","model":"gpt-5-codex"}`)
	if err := runTestCLIWithStdin(context.Background(), []string{
		"--store", filepath.Join(t.TempDir(), "sessions.json"),
		"--json", "report", "claude", "--raw-stdin-defaults-only", "--no-tmux",
	}, stdin, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["status"] != "ignored" {
		t.Fatalf("result = %q, decoded=%#v, err=%v", stdout.String(), result, err)
	}
}

func TestInferredNativeEndCannotBeResurrectedByProcessEvidence(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	process := &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}

	start, err := prepareReport(
		strings.NewReader(`{"session_id":"codex-session","cwd":"/work","hook_event_name":"SessionStart","source":"startup","model":"gpt-5"}`),
		reportOptions{harness: "codex", activity: "idle", rawDefaultsOnly: true},
		reportRuntimeContext{defaultObservedAt: at},
	)
	if err != nil {
		t.Fatal(err)
	}
	start.observation.SetProcess(process)
	session, err := store.Observe(context.Background(), start.observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceLive {
		t.Fatalf("start presence = %q, want live", session.Presence())
	}

	end, err := prepareReport(
		strings.NewReader(`{"session_id":"codex-session","cwd":"/work","hook_event_name":"SessionEnd","reason":"other","model":"gpt-5"}`),
		reportOptions{harness: "codex", rawDefaultsOnly: true},
		reportRuntimeContext{defaultObservedAt: at.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	end.observation.SetProcess(process)
	session, err = store.Observe(context.Background(), end.observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone || session.Activity() != nil {
		t.Fatalf("end state = %#v", session)
	}

	present := true
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{SessionID: "codex-session"}, Evidence: &registry.Sighting{Process: *process, Present: present}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone {
		t.Fatalf("process evidence resurrected ended session: %#v", session)
	}
}

type lifecycleReportCase struct {
	name         string
	lifecycle    string
	presence     string
	activity     string
	wantPresence registry.Presence
	wantActivity *registry.Activity
}

func TestOpenClawLifecycleReportsDriveDocumentedStateTransitions(t *testing.T) {
	t.Parallel()

	tests := []lifecycleReportCase{
		{name: "session_start", lifecycle: "start", presence: "live", activity: "idle", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityIdle)},
		{name: "before_agent_run", lifecycle: "", presence: "live", activity: "running", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityRunning)},
		{name: "agent_end", lifecycle: "", presence: "live", activity: "idle", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityIdle)},
		{name: "session_end", lifecycle: "end", presence: "gone", activity: "", wantPresence: registry.PresenceGone, wantActivity: nil},
	}
	testLifecycleReports(t, "openclaw", time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), tests)
}

func TestHermesLifecycleReportsDriveDocumentedStateTransitions(t *testing.T) {
	t.Parallel()

	tests := []lifecycleReportCase{
		{name: "on_session_start", lifecycle: "start", presence: "live", activity: "idle", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityIdle)},
		{name: "pre_llm_call", lifecycle: "", presence: "live", activity: "running", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityRunning)},
		{name: "pre_approval_request", lifecycle: "", presence: "live", activity: "waiting", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityWaiting)},
		{name: "post_approval_response", lifecycle: "", presence: "live", activity: "running", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityRunning)},
		{name: "on_session_end", lifecycle: "", presence: "live", activity: "idle", wantPresence: registry.PresenceLive, wantActivity: new(registry.ActivityIdle)},
		{name: "on_session_finalize", lifecycle: "end", presence: "gone", activity: "", wantPresence: registry.PresenceGone, wantActivity: nil},
	}
	testLifecycleReports(t, "hermes", time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), tests)
}

func testLifecycleReports(t *testing.T, harness string, base time.Time, tests []lifecycleReportCase) {
	t.Helper()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	for index, test := range tests {
		prepared, err := prepareReport(nil, reportOptions{
			harness: harness, lifecycle: test.lifecycle, presence: test.presence, activity: test.activity,
			sessionID: harness + "-session", event: test.name,
		}, reportRuntimeContext{defaultObservedAt: base.Add(time.Duration(index) * time.Second)})
		if err != nil {
			t.Fatalf("preparing %s report: %v", test.name, err)
		}
		session, err := store.Observe(context.Background(), prepared.observation)
		if err != nil {
			t.Fatalf("recording %s report: %v", test.name, err)
		}
		if session.Presence() != test.wantPresence || !equalActivity(session.Activity(), test.wantActivity) {
			t.Fatalf("%s state = presence %q activity %#v", test.name, session.Presence(), session.Activity())
		}
	}
}

func equalActivity(left, right *registry.Activity) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}
