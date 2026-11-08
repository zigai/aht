package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/reporting"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestReportQuietSuppressesHumanOutput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if err := runTestCLI(
		context.Background(),
		[]string{
			"--store",
			t.TempDir() + "/sessions.json",
			"report",
			"codex",
			"--session-id",
			"json",
			"--event",
			"start",
			"--quiet",
		},
		&stdout,
		&stderr,
	); err != nil {
		t.Fatal(err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("quiet report wrote output: %q", stdout.String())
	}
}

func TestReportCommandDefaultsToHumanOutput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := runTestCLI(
		context.Background(),
		[]string{
			"--store",
			filepath.Join(t.TempDir(), "sessions.json"),
			"report",
			"codex",
			"--session-id",
			"human",
			"--event",
			"start",
			"--no-tmux",
		},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}

	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") ||
		!strings.Contains(stdout.String(), "codex") {
		t.Fatalf("report default output = %q", stdout.String())
	}
}

func TestReportHumanOutputDistinguishesReportedAndEffectiveActivity(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := runTestCLI(
		context.Background(),
		[]string{
			"--store",
			filepath.Join(t.TempDir(), "sessions.json"),
			"report",
			"codex",
			"--session-id",
			"activity",
			"--event",
			"turn_complete",
			"--activity",
			"waiting",
			"--no-tmux",
		},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
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
	if err := runTestCLI(
		context.Background(),
		[]string{
			"--store",
			filepath.Join(t.TempDir(), "sessions.json"),
			"--json",
			"report",
			"codex",
			"--session-id",
			"machine",
			"--event",
			"start",
			"--no-tmux",
		},
		&stdout,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}

	var session registry.Session
	if err := json.Unmarshal(
		stdout.Bytes(),
		&session,
	); err != nil ||
		session.SchemaVersion != registry.StoreSchemaVersion {
		t.Fatalf("report JSON = %q, %v", stdout.String(), err)
	}
}

func TestReportJSONCoversIgnoredResult(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	stdin := strings.NewReader(
		`{"session_id":"codex-session","transcript_path":"/home/user/` +
			`.codex/sessions/rollout.jsonl","hook_event_name":"Stop",` +
			`"model":"gpt-5-codex"}`,
	)
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

func TestReportQwenSubagentHooksPreserveForegroundState(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"waiting", "idle"} {
		for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"} {
			t.Run(state+"/"+event, func(t *testing.T) {
				t.Parallel()
				assertQwenSubagentReportPreservesSession(t, state, event)
			})
		}
	}
}

func TestInferredNativeEndCannotBeResurrectedByProcessEvidence(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	process := &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}

	start, err := reporting.Prepare(
		strings.NewReader(
			`{"session_id":"codex-session","cwd":"/work",`+
				`"hook_event_name":"SessionStart","source":"startup",`+
				`"model":"gpt-5"}`,
		),
		reporting.Options{Harness: "codex", Activity: "idle", RawDefaultsOnly: true},
		reporting.Runtime{DefaultObservedAt: at},
	)
	if err != nil {
		t.Fatal(err)
	}

	start.Observation.SetProcess(process)

	session, err := store.Observe(context.Background(), start.Observation)
	if err != nil {
		t.Fatal(err)
	}

	if session.Presence() != registry.PresenceLive {
		t.Fatalf("start presence = %q, want live", session.Presence())
	}

	end, err := reporting.Prepare(
		strings.NewReader(
			`{"session_id":"codex-session","cwd":"/work",`+
				`"hook_event_name":"SessionEnd","reason":"other",`+
				`"model":"gpt-5"}`,
		),
		reporting.Options{Harness: "codex", RawDefaultsOnly: true},
		reporting.Runtime{DefaultObservedAt: at.Add(time.Second)},
	)
	if err != nil {
		t.Fatal(err)
	}

	end.Observation.SetProcess(process)

	session, err = store.Observe(context.Background(), end.Observation)
	if err != nil {
		t.Fatal(err)
	}

	if session.Presence() != registry.PresenceGone || session.Activity() != nil {
		t.Fatalf("end state = %#v", session)
	}

	present := true

	session, err = store.Observe(
		context.Background(),
		registry.Observation{
			Harness:  registry.Harness("codex"),
			At:       at.Add(2 * time.Second),
			Subject:  registry.ObservationIdentity{SessionID: "codex-session"},
			Evidence: &registry.Sighting{Process: *process, Present: present},
		},
	)
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
		{
			name:         "session_start",
			lifecycle:    "start",
			presence:     "live",
			activity:     "idle",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityIdle),
		},
		{
			name:         "before_agent_run",
			lifecycle:    "",
			presence:     "live",
			activity:     "running",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityRunning),
		},
		{
			name:         "agent_end",
			lifecycle:    "",
			presence:     "live",
			activity:     "idle",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityIdle),
		},
		{
			name:         "session_end",
			lifecycle:    "end",
			presence:     "gone",
			activity:     "",
			wantPresence: registry.PresenceGone,
			wantActivity: nil,
		},
	}
	testLifecycleReports(t, "openclaw", time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC), tests)
}

func TestHermesLifecycleReportsDriveDocumentedStateTransitions(t *testing.T) {
	t.Parallel()

	tests := []lifecycleReportCase{
		{
			name:         "on_session_start",
			lifecycle:    "start",
			presence:     "live",
			activity:     "idle",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityIdle),
		},
		{
			name:         "pre_llm_call",
			lifecycle:    "",
			presence:     "live",
			activity:     "running",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityRunning),
		},
		{
			name:         "pre_approval_request",
			lifecycle:    "",
			presence:     "live",
			activity:     "waiting",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityWaiting),
		},
		{
			name:         "post_approval_response",
			lifecycle:    "",
			presence:     "live",
			activity:     "running",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityRunning),
		},
		{
			name:         "on_session_end",
			lifecycle:    "",
			presence:     "live",
			activity:     "idle",
			wantPresence: registry.PresenceLive,
			wantActivity: new(registry.ActivityIdle),
		},
		{
			name:         "on_session_finalize",
			lifecycle:    "end",
			presence:     "gone",
			activity:     "",
			wantPresence: registry.PresenceGone,
			wantActivity: nil,
		},
	}
	testLifecycleReports(t, "hermes", time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), tests)
}

func assertQwenSubagentReportPreservesSession(t *testing.T, state string, event string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.json")
	args := []string{
		"--store",
		path,
		"--json",
		"report",
		"qwen",
		"--raw-stdin-defaults-only",
		"--no-tmux",
		"--session-id",
		"native-session",
		"--activity",
		state,
	}

	foreground := `{"session_id":"native-session","cwd":"/work",` +
		`"hook_event_name":"Notification",` +
		`"notification_type":"idle_prompt"}`
	if state == "waiting" {
		foreground = `{"session_id":"native-session","cwd":"/work",` +
			`"hook_event_name":"Notification",` +
			`"notification_type":"permission_prompt"}`
	}

	var output bytes.Buffer
	if err := runTestCLIWithStdin(
		t.Context(),
		args,
		strings.NewReader(foreground),
		&output,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}

	var before registry.Session
	if err := json.Unmarshal(output.Bytes(), &before); err != nil {
		t.Fatal(err)
	}

	args[len(args)-1] = "running"
	payload := `{"session_id":"native-session","cwd":"/work",` +
		`"hook_event_name":"` + event + `","agent_id":"child-agent","tool_name":"read_file"}`

	output.Reset()

	if err := runTestCLIWithStdin(
		t.Context(),
		args,
		strings.NewReader(payload),
		&output,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "ignored" {
		t.Fatalf("subagent result = %s, error = %v", output.Bytes(), err)
	}

	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, error = %v", sessions, err)
	}

	if diff := cmp.Diff(
		before,
		sessions[0],
		cmp.Comparer(func(before, after registry.ProcessIdentity) bool { return before == after }),
	); diff != "" {
		t.Fatalf("subagent changed foreground session (-before +after):\n%s", diff)
	}
}

func testLifecycleReports(
	t *testing.T,
	harness string,
	base time.Time,
	tests []lifecycleReportCase,
) {
	t.Helper()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})

	for index, test := range tests {
		prepared, err := reporting.Prepare(nil, reporting.Options{
			Harness:   harness,
			Lifecycle: test.lifecycle,
			Presence:  test.presence,
			Activity:  test.activity,
			SessionID: harness + "-session",
			Event:     test.name,
		}, reporting.Runtime{DefaultObservedAt: base.Add(time.Duration(index) * time.Second)})
		if err != nil {
			t.Fatalf("preparing %s report: %v", test.name, err)
		}

		session, err := store.Observe(context.Background(), prepared.Observation)
		if err != nil {
			t.Fatalf("recording %s report: %v", test.name, err)
		}

		if session.Presence() != test.wantPresence ||
			!equalActivity(session.Activity(), test.wantActivity) {
			t.Fatalf(
				"%s state = presence %q activity %#v",
				test.name,
				session.Presence(),
				session.Activity(),
			)
		}
	}
}

func equalActivity(left *registry.Activity, right *registry.Activity) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

func TestReportIgnoresAnotherHarnessEnvironment(t *testing.T) {
	agent := startNamedProcess(t, "pi")
	t.Setenv("CLAUDE_PID", strconv.Itoa(agent))

	session := reportFromEnvironment(t, "--session-id", "inner-pi")
	if session.Process != nil && session.Process.PID == agent {
		t.Fatal("pi report took its process from an outer Claude session's PID")
	}
}

func TestReportReadsTheReportingHarnessEnvironment(t *testing.T) {
	agent := startNamedProcess(t, "pi")
	t.Setenv("CLAUDE_SESSION_ID", "outer-claude")
	t.Setenv("PI_SESSION_ID", "inner-pi")
	t.Setenv("PI_PID", strconv.Itoa(agent))

	session := reportFromEnvironment(t)
	if session.SessionID != "inner-pi" {
		t.Fatalf("session id = %q, want inner-pi", session.SessionID)
	}

	if session.Process == nil || session.Process.PID != agent {
		t.Fatalf("process = %+v, want pid %d", session.Process, agent)
	}
}

func TestReportPrefersFlagsToNativeEnvironment(t *testing.T) {
	t.Setenv("PI_SESSION_ID", "native")

	if session := reportFromEnvironment(t); session.SessionID != "native" {
		t.Fatalf("session id = %q", session.SessionID)
	}

	if session := reportFromEnvironment(
		t,
		"--session-id",
		"flag",
	); session.SessionID != "flag" {
		t.Fatalf("session id = %q", session.SessionID)
	}
}

func reportFromEnvironment(t *testing.T, extra ...string) registry.Session {
	t.Helper()

	args := append(
		[]string{
			"--store",
			filepath.Join(t.TempDir(), "sessions.json"),
			"--json",
			"report",
			"pi",
			"--event",
			"session_start",
			"--no-tmux",
		},
		extra...,
	)

	var stdout bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var session registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("report output %q: %v", stdout.String(), err)
	}

	return session
}

func startNamedProcess(t *testing.T, name string) int {
	t.Helper()

	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}

	cmd := &exec.Cmd{Path: path, Args: []string{name, "60"}}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return cmd.Process.Pid
}

func TestReportKeepsExplicitProjectRootWhenPayloadCarriesCWD(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
	}{
		{name: "droid environment", env: map[string]string{"FACTORY_PROJECT_DIR": "/srv/project"}},
		{name: "flag", args: []string{"--project-root", "/srv/project"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			payload, err := json.Marshal(map[string]string{
				"session_id":      "droid-1",
				"cwd":             t.TempDir(),
				"hook_event_name": "SessionStart",
			})
			if err != nil {
				t.Fatal(err)
			}

			args := append([]string{"--raw-stdin-defaults-only"}, tt.args...)

			session := reportWithStdin(t, "droid", bytes.NewReader(payload), args...)
			if session.ProjectRoot != "/srv/project" {
				t.Fatalf("project root = %q, want /srv/project", session.ProjectRoot)
			}
		})
	}
}

func TestReportDerivesProjectRootFromCWDWhenNoneIsGiven(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}

	work := filepath.Join(repo, "pkg", "sub")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Run("cwd flag", func(t *testing.T) {
		session := reportWithStdin(t, "codex", nil, "--session-id", "cwd-flag", "--cwd", work)
		if session.ProjectRoot != repo {
			t.Fatalf("project root = %q, want %q", session.ProjectRoot, repo)
		}
	})

	t.Run("working directory", func(t *testing.T) {
		t.Chdir(work)

		session := reportWithStdin(t, "codex", nil, "--session-id", "working-directory")
		if session.ProjectRoot != repo {
			t.Fatalf("project root = %q, want %q", session.ProjectRoot, repo)
		}
	})
}

func reportWithStdin(
	t *testing.T,
	harnessID string,
	stdin io.Reader,
	extra ...string,
) registry.Session {
	t.Helper()

	args := make([]string, 0, 8+len(extra))
	args = append(
		args,
		"--store",
		filepath.Join(t.TempDir(), "sessions.json"),
		"--json",
		"report",
		harnessID,
	)
	args = append(args, "--event", "session_start", "--no-tmux")
	args = append(args, extra...)

	var stdout, stderr bytes.Buffer
	if err := runTestCLIWithStdin(t.Context(), args, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("report: %v: %s", err, stderr.String())
	}

	var session registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("report output %q: %v", stdout.String(), err)
	}

	return session
}

func TestReportSucceedsOnceWhenBrokerRecordsAndClosesBeforeReplying(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")

	listener := listenFakeBroker(t)

	applied := make(chan error, 1)

	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			applied <- acceptErr
			return
		}

		defer func() { _ = connection.Close() }()

		var request broker.Request
		if decodeErr := json.NewDecoder(connection).Decode(&request); decodeErr != nil {
			applied <- decodeErr
			return
		}

		_, observeErr := registry.NewJournal(storePath, catalog.Rules{}).
			Observe(context.Background(), *request.Observation)
		applied <- observeErr
	}()

	var stdout, stderr bytes.Buffer

	reportErr := runTestCLI(
		t.Context(),
		[]string{
			"--store", storePath, "--json", "report", "codex",
			"--session-id", "s1",
			"--event", "start",
			"--activity", "running",
			"--sequence", "5",
			"--reporter", "probe",
			"--no-tmux",
		},
		&stdout,
		&stderr,
	)

	select {
	case err := <-applied:
		if err != nil {
			t.Fatalf("fake broker did not record the report: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("report never reached the fake broker")
	}

	if reportErr != nil {
		t.Fatalf("report after broker recorded it and closed: %v", reportErr)
	}

	sessions, err := registry.NewJournal(storePath, catalog.Rules{}).
		List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].SessionID != "s1" {
		t.Fatalf("sessions = %+v, want only s1", sessions)
	}
}
