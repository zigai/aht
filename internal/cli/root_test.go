package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
)

const expectedSessionSchemaVersion = 2

//nolint:cyclop // assertions independently verify each report dimension
func TestPrepareReportCarriesIndependentDimensions(t *testing.T) {
	t.Parallel()
	prepared, err := prepareReport(strings.NewReader(`{"session_id":"session-1","cwd":"/work","hook_event_name":"PermissionRequest","model":"gpt-5"}`), reportOptions{
		harness: "codex", presence: "live", activity: "waiting", sessionID: "session-1", event: "permission_prompt",
		cwd: "/work", projectRoot: "/work", resumeCommand: []string{"codex", "resume", "session-1"}, rawStdin: true,
	}, reportRuntimeContext{
		tmux:              registry.TmuxContext{Inside: true, SessionName: "dev", PaneID: "%4"},
		defaultObservedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Presence == nil || *prepared.observation.Presence != registry.PresenceLive || prepared.observation.Activity == nil || *prepared.observation.Activity != registry.ActivityWaiting {
		t.Fatalf("independent dimensions lost: %#v", prepared.observation)
	}
	if prepared.observation.ActivityAuthoritative == nil || *prepared.observation.ActivityAuthoritative {
		t.Fatalf("Codex hook activity must be stored as a non-authoritative hint: %#v", prepared.observation)
	}
	if prepared.observation.Catalog == nil || len(prepared.observation.Catalog.ResumeCommand) != 3 {
		t.Fatalf("catalog metadata missing: %#v", prepared.observation.Catalog)
	}
	if prepared.observation.Tmux == nil || prepared.observation.Tmux.SessionName != "dev" || prepared.observation.Tmux.PaneID != "%4" {
		t.Fatalf("tmux context missing: %#v", prepared.observation.Tmux)
	}
	if len(prepared.observation.RawPayload) == 0 {
		t.Fatal("raw payload was not preserved")
	}
}

func TestPrepareReportIncludesNativeMultiplexerContext(t *testing.T) {
	t.Parallel()
	location := registry.MultiplexerContext{Kind: registry.MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"}
	prepared, err := prepareReport(nil, reportOptions{
		harness: "codex", sessionID: "session", event: "turn_complete",
	}, reportRuntimeContext{multiplexer: location, defaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Multiplexer == nil || *prepared.observation.Multiplexer != location {
		t.Fatalf("multiplexer context = %#v", prepared.observation.Multiplexer)
	}
}

func TestPrepareReportCarriesNativeLifecycle(t *testing.T) {
	t.Parallel()

	prepared, err := prepareReport(nil, reportOptions{
		harness: "openclaw", lifecycle: "resume", presence: "live", activity: "idle",
		sessionID: "native-session", event: "session_start",
	}, reportRuntimeContext{defaultObservedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Lifecycle == nil || *prepared.observation.Lifecycle != registry.NativeLifecycleResume {
		t.Fatalf("native lifecycle missing: %#v", prepared.observation)
	}
}

func TestPrepareReportInfersLifecycleFromNativeEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		options     reportOptions
		payload     string
		lifecycle   registry.NativeLifecycle
		presence    registry.Presence
		nativeEvent string
	}{
		{
			name: "codex startup",
			options: reportOptions{
				harness: "codex", activity: "idle", rawDefaultsOnly: true,
			},
			payload:     `{"session_id":"codex-start","cwd":"/work","hook_event_name":"SessionStart","source":"startup","model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleStart,
			presence:    registry.PresenceLive,
			nativeEvent: "SessionStart",
		},
		{
			name: "codex resume",
			options: reportOptions{
				harness: "codex", activity: "idle", rawDefaultsOnly: true,
			},
			payload:     `{"session_id":"codex-resume","cwd":"/work","hook_event_name":"SessionStart","source":"resume","model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleResume,
			presence:    registry.PresenceLive,
			nativeEvent: "SessionStart",
		},
		{
			name: "codex end",
			options: reportOptions{
				harness: "codex", rawDefaultsOnly: true,
			},
			payload:     `{"session_id":"codex-end","cwd":"/work","hook_event_name":"SessionEnd","reason":"other","model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleEnd,
			presence:    registry.PresenceGone,
			nativeEvent: "SessionEnd",
		},
		{
			name: "pi resume",
			options: reportOptions{
				harness: "pi", activity: "idle", event: "session_start", sessionPath: "/tmp/pi-session.json",
				attributes: []string{"pi_reason=resume"},
			},
			lifecycle:   registry.NativeLifecycleResume,
			presence:    registry.PresenceLive,
			nativeEvent: "session_start",
		},
		{
			name: "opencode deleted",
			options: reportOptions{
				harness: "opencode", event: "session.deleted", sessionID: "open-session",
			},
			lifecycle:   registry.NativeLifecycleEnd,
			presence:    registry.PresenceGone,
			nativeEvent: "session.deleted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, err := prepareReport(
				strings.NewReader(test.payload),
				test.options,
				reportRuntimeContext{defaultObservedAt: time.Now().UTC()},
			)
			if err != nil {
				t.Fatal(err)
			}
			observation := prepared.observation
			if observation.Lifecycle == nil || *observation.Lifecycle != test.lifecycle {
				t.Fatalf("lifecycle = %#v, want %q", observation.Lifecycle, test.lifecycle)
			}
			if observation.Presence == nil || *observation.Presence != test.presence {
				t.Fatalf("presence = %#v, want %q", observation.Presence, test.presence)
			}
			if observation.NativeEvent != test.nativeEvent {
				t.Fatalf("native event = %q, want %q", observation.NativeEvent, test.nativeEvent)
			}
		})
	}
}

func TestInferredNativeEndCannotBeResurrectedByProcessEvidence(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
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
	start.observation.Process = process
	session, err := store.Observe(context.Background(), start.observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceLive {
		t.Fatalf("start presence = %q, want live", session.Presence)
	}

	end, err := prepareReport(
		strings.NewReader(`{"session_id":"codex-session","cwd":"/work","hook_event_name":"SessionEnd","reason":"other","model":"gpt-5"}`),
		reportOptions{harness: "codex", rawDefaultsOnly: true},
		reportRuntimeContext{defaultObservedAt: at.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}
	end.observation.Process = process
	session, err = store.Observe(context.Background(), end.observation)
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone || session.Activity != nil {
		t.Fatalf("end state = %#v", session)
	}

	present := true
	session, err = store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "codex-session"},
		ProcessPresent: &present, Process: process, ObservedAt: at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone {
		t.Fatalf("process evidence resurrected ended session: %#v", session)
	}
}

func TestPrepareReportAddsNativeResumeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options reportOptions
		want    []string
	}{
		{
			name:    "codex id",
			options: reportOptions{harness: "codex", event: "turn", sessionID: "codex-session"},
			want:    []string{"codex", "resume", "codex-session"},
		},
		{
			name:    "pi path",
			options: reportOptions{harness: "pi", event: "agent_settled", sessionPath: "/tmp/pi-session.json"},
			want:    []string{"pi", "--session", "/tmp/pi-session.json"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, err := prepareReport(nil, test.options, reportRuntimeContext{defaultObservedAt: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.observation.Catalog == nil || !slices.Equal(prepared.observation.Catalog.ResumeCommand, test.want) {
				t.Fatalf("resume command = %#v, want %#v", prepared.observation.Catalog, test.want)
			}
		})
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

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
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
		if session.Presence != test.wantPresence || !equalActivity(session.Activity, test.wantActivity) {
			t.Fatalf("%s state = presence %q activity %#v", test.name, session.Presence, session.Activity)
		}
	}
}

func equalActivity(left, right *registry.Activity) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

func TestPrepareReportAttachesMatchingAgentProcess(t *testing.T) {
	t.Parallel()
	agent := processinfo.Process{
		PID:            42,
		PPID:           10,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/node",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
		Args:           []string{"pi"},
	}
	prepared, err := prepareReport(nil, reportOptions{
		harness: "pi", activity: "running", sessionPath: "/tmp/session.json",
	}, reportRuntimeContext{processes: []processinfo.Process{
		{PID: 50, PPID: 42, StartIdentity: "boot:50", Executable: "/bin/sh", Args: []string{"sh"}},
		agent,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Process == nil || prepared.observation.Process.PID != agent.PID || prepared.observation.Process.StartIdentity != agent.StartIdentity {
		t.Fatalf("report process identity = %#v, want agent process %#v", prepared.observation.Process, agent)
	}
}

func TestPrepareReportProcessEvidenceRequiresCompleteIdentity(t *testing.T) {
	t.Parallel()
	_, err := prepareReport(bytes.NewReader(nil), reportOptions{harness: "codex", evidence: "process", sessionID: "session-1", pid: 12}, reportRuntimeContext{})
	if err == nil {
		t.Fatal("expected incomplete process identity error")
	}
}

func TestPrepareReportProcessEvidenceDoesNotCarryNativeAuthority(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/4"}
	prepared, err := prepareReport(nil, reportOptions{
		harness: "codex", presence: "live", evidence: "process", pid: process.PID, event: "process.start",
	}, reportRuntimeContext{processes: []processinfo.Process{process}, defaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	observation := prepared.observation
	if observation.Source != registry.ObservationSourceProcess || observation.ActivityAuthoritative != nil || observation.Activity != nil || observation.NativeEvent != "" {
		t.Fatalf("process observation retained native fields: %#v", observation)
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("process observation is invalid: %v", err)
	}
}

func TestShimProcessReportsInferIdentityAndTransitionState(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/bin/sh", CWD: "/work", TTY: "/dev/pts/4"}
	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	base := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	var sessionID string
	for index, test := range []struct {
		name     string
		presence string
		present  bool
	}{
		{name: "start", presence: "live", present: true},
		{name: "exit", presence: "gone", present: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sessionID = requireShimProcessTransition(
				t, store, process, test.name, test.presence, test.present,
				base.Add(time.Duration(index)*time.Second), sessionID,
			)
		})
	}
}

func requireShimProcessTransition(
	t *testing.T,
	store *registry.FileStore,
	process processinfo.Process,
	name, presence string,
	present bool,
	observedAt time.Time,
	previousSessionID string,
) string {
	t.Helper()
	prepared, err := prepareReport(nil, reportOptions{
		harness: "droid", presence: presence, evidence: "process", pid: process.PID, event: "process." + name,
	}, reportRuntimeContext{processes: []processinfo.Process{process}, defaultObservedAt: observedAt})
	if err != nil {
		t.Fatal(err)
	}
	observation := prepared.observation
	requireShimObservation(t, observation, process, present)
	session, err := store.Observe(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if previousSessionID != "" && session.ID != previousSessionID {
		t.Fatalf("shim transitions split sessions: start=%q next=%q", previousSessionID, session.ID)
	}
	wantPresence := registry.PresenceLive
	if !present {
		wantPresence = registry.PresenceGone
	}
	if session.Presence != wantPresence {
		t.Fatalf("session presence = %q, want %q", session.Presence, wantPresence)
	}
	return session.ID
}

func requireShimObservation(t *testing.T, observation registry.Observation, process processinfo.Process, present bool) {
	t.Helper()
	if observation.Source != registry.ObservationSourceProcess || observation.Process == nil || !observation.Process.Complete() || observation.Process.StartIdentity != process.StartIdentity {
		t.Fatalf("shim process identity = %#v", observation.Process)
	}
	if observation.ProcessPresent == nil || *observation.ProcessPresent != present {
		t.Fatalf("process presence = %#v, want %v", observation.ProcessPresent, present)
	}
}

func TestPrepareReportRejectsConflictingStdinModes(t *testing.T) {
	t.Parallel()
	_, err := prepareReport(strings.NewReader(`{}`), reportOptions{harness: "codex", rawStdin: true, rawDefaultsOnly: true}, reportRuntimeContext{})
	if !errors.Is(err, errConflictingReportStdin) {
		t.Fatalf("stdin mode error = %v", err)
	}
}

func TestPrepareReportRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	_, err := prepareReport(
		strings.NewReader(strings.Repeat("x", maxPayloadInputBytes+1)),
		reportOptions{harness: "codex", rawStdin: true},
		reportRuntimeContext{},
	)
	if !errors.Is(err, errPayloadInputTooLarge) {
		t.Fatalf("error = %v, want %v", err, errPayloadInputTooLarge)
	}
}

func TestPrepareReportAcceptsLargeCodexPostToolUseDefaults(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"codex-image","transcript_path":null,"cwd":"/work","hook_event_name":"PostToolUse","model":"gpt-5","tool_name":"view_image","tool_response":"` +
		strings.Repeat("x", maxPayloadInputBytes) + `"}`
	prepared, err := prepareReport(
		strings.NewReader(payload),
		reportOptions{harness: "codex", activity: "running", rawDefaultsOnly: true},
		reportRuntimeContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Identity.SessionID != "codex-image" || prepared.observation.NativeEvent != "PostToolUse" {
		t.Fatalf("PostToolUse metadata = %#v", prepared.observation)
	}
	if len(prepared.observation.RawPayload) != 0 {
		t.Fatalf("PostToolUse raw payload was retained: %d bytes", len(prepared.observation.RawPayload))
	}
}

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

func TestInfoCommandUsesHumanOutputUnlessJSONRequested(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/sessions.json"
	store := registry.NewFileStore(path)
	at := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	activity := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "session-1"},
		NativeEvent: "Stop", Activity: &activity, ObservedAt: at,
	})
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

func TestVersionHonorsJSONFlag(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--json", "--version"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON version: %v; output=%q", err, stdout.String())
	}
	if result["version"] == "" {
		t.Fatalf("missing version in %#v", result)
	}
}

func TestVersionDefaultsToHumanOutput(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.HasPrefix(stdout.String(), "aht ") {
		t.Fatalf("version default output = %q", stdout.String())
	}
}

func TestListTableColumnsExpandsSessionAndCWDWhenWidthAllows(t *testing.T) {
	t.Parallel()
	rows := [][]string{
		{"omp-5afa9c61", "omp", "Format watch command column alignment", "live", "running", "tmux:0:2:zsh:%1", "~/Projects/sample-project", "1s ago"},
		{"pi-ea2cacd9", "pi", "2026-08-27T20-22-44-492Z_01a044e3-a40c-77dc-8593-f0f6a3a7c42f", "live", "idle", "tmux:0:3:zsh:%2", "~/Projects/config", "1s ago"},
	}

	// In a wide terminal (e.g. 200 columns), SESSION and CWD should not be truncated.
	wideCols := listTableColumns(rows, 200)
	sessionCol := wideCols[2]
	cwdCol := wideCols[6]
	if sessionCol.width < len("2026-08-27T20-22-44-492Z_01a044e3-a40c-77dc-8593-f0f6a3a7c42f") {
		t.Fatalf("session width in wide terminal = %d, want >= 60", sessionCol.width)
	}
	if cwdCol.width < len("~/Projects/sample-project") {
		t.Fatalf("CWD width in wide terminal = %d, want >= 25", cwdCol.width)
	}

	// In standard 120 width, SESSION and CWD get dynamic proportioned widths instead of static 14/18.
	stdCols := listTableColumns(rows, 120)
	if stdCols[2].width < 25 {
		t.Fatalf("session width in 120 terminal = %d, want >= 25", stdCols[2].width)
	}
	if stdCols[6].width < 15 {
		t.Fatalf("CWD width in 120 terminal = %d, want >= 15", stdCols[6].width)
	}
}

func TestListFullFlagRendersCompleteValues(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewFileStore(path)
	now := time.Now().UTC()
	live := registry.PresenceLive
	longSession := "Deploy new analytics dashboard to production cluster for quarterly report"
	longPath := "/home/zigai/Projects/very/deeply/nested/repository/path/with/lots/of/subdirectories"
	session, err := store.Observe(context.Background(), registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: longSession},
		Presence: &live, NativeEvent: "start", ObservedAt: now,
		Catalog: &registry.CatalogMetadata{CWD: longPath},
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list", "--full"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Contains(output, "…") {
		t.Fatalf("full output contained truncation ellipsis: %q", output)
	}
	for _, value := range []string{session.ID, "Deploy", "analytics", "dashboard", "production", "quarterly", "report", "subdirectories"} {
		if !strings.Contains(output, value) {
			t.Fatalf("full output missing %q: %q", value, output)
		}
	}
}

func TestListFullLayoutUsesTableOnlyWhenUsefulColumnsFit(t *testing.T) {
	t.Parallel()
	rows := [][]string{{
		"omp-8123f29b-full-identifier",
		"omp",
		"2026-08-29T07-13-53-424Z_01a04c5e-2510-7000-86b9-e9be6ca73e54.jsonl",
		"live",
		"idle",
		"tmux:sesh:5:zsh:%21",
		"~/Projects/omp-extensions",
		"4h ago",
	}}
	columns, fits := listFullTableColumns(rows, 120)
	if fits {
		t.Fatal("full table unexpectedly fit in 120 columns")
	}
	if columns[2].width < 24 || columns[6].width < 20 {
		t.Fatalf("full table used unreadable flexible widths: session=%d CWD=%d", columns[2].width, columns[6].width)
	}

	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	if err := app.writeStackedHumanRows(columns, rows); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Contains(output, "…") || !strings.Contains(output, "Session:") || !strings.Contains(output, rows[0][2]) {
		t.Fatalf("stacked full output lost data: %q", output)
	}

	wideColumns, wideFits := listFullTableColumns(rows, 200)
	if !wideFits {
		t.Fatal("full table did not fit in 200 columns")
	}
	if err := validateHumanColumns(wideColumns, 200); err != nil {
		t.Fatalf("wide full table columns invalid: %v", err)
	}
	if got := strings.Join(wrapHumanSession(rows[0][2], wideColumns[2].width), ""); got != rows[0][2] {
		t.Fatalf("semantic session wrapping lost data: got %q", got)
	}
}

func TestSessionDisplayLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session registry.Session
		want    string
	}{
		{
			name:    "explicit session id",
			session: registry.Session{ID: "omp-12345678", SessionID: "custom-thread-name", SessionPath: "/tmp/path.jsonl"},
			want:    "custom-thread-name",
		},
		{
			name:    "omp timestamped jsonl path extracts uuid",
			session: registry.Session{ID: "omp-12345678", SessionPath: "/home/zigai/.omp/agent/sessions/-Projects-aht/2026-08-29T10-11-12-300Z_01a04d00-7b2c-7000-8cff-61086b324bf2.jsonl"},
			want:    "01a04d00-7b2c-7000-8cff-61086b324bf2",
		},
		{
			name:    "plain filename fallback",
			session: registry.Session{ID: "omp-12345678", SessionPath: "/tmp/my-transcript.jsonl"},
			want:    "my-transcript.jsonl",
		},
		{
			name:    "pane id fallback",
			session: registry.Session{ID: "omp-12345678", Multiplexer: registry.MultiplexerContext{PaneID: "%12"}},
			want:    "%12",
		},
		{
			name:    "process pid fallback",
			session: registry.Session{ID: "omp-12345678", Process: &registry.ProcessIdentity{PID: 42189}},
			want:    "pid:42189",
		},
		{
			name:    "short id fallback",
			session: registry.Session{ID: "omp-1234567890abcdef"},
			want:    "omp-12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionDisplayLabel(tt.session); got != tt.want {
				t.Fatalf("sessionDisplayLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
