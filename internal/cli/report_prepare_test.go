package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/registry"
)

const expectedSessionSchemaVersion = 3

//nolint:cyclop // assertions independently verify each report dimension
func TestPrepareReportCarriesIndependentDimensions(t *testing.T) {
	t.Parallel()
	prepared, err := prepareReport(strings.NewReader(`{"session_id":"session-1","cwd":"/work","hook_event_name":"PermissionRequest","model":"gpt-5"}`), reportOptions{
		harness: "codex", presence: "live", activity: "waiting", sessionID: "session-1", event: "permission_prompt",
		cwd: "/work", projectRoot: "/work", resumeCommand: []string{"codex", "resume", "session-1"}, rawStdin: true,
	}, reportRuntimeContext{
		tmux:              registry.Location{Kind: registry.MultiplexerTmux, SessionName: "dev", PaneID: "%4"},
		defaultObservedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Report().Claim == nil || *prepared.observation.Report().Claim != registry.PresenceLive || prepared.observation.ActivityClaim() == nil || *prepared.observation.ActivityClaim() != registry.ActivityWaiting {
		t.Fatalf("independent dimensions lost: %#v", prepared.observation)
	}
	if (catalog.Rules{}).Policy(prepared.observation.Harness).Authority != registry.AuthorityScreen {
		t.Fatalf("Codex hook activity must be stored as a non-authoritative hint: %#v", prepared.observation)
	}
	if prepared.observation.Listing() == nil || len(prepared.observation.Listing().ResumeCommand) != 3 {
		t.Fatalf("catalog metadata missing: %#v", prepared.observation.Listing())
	}
	if prepared.observation.Location() == nil || prepared.observation.Location().SessionName != "dev" || prepared.observation.Location().PaneID != "%4" {
		t.Fatalf("tmux context missing: %#v", prepared.observation.Location())
	}
	if len(prepared.observation.Report().Payload) == 0 {
		t.Fatal("raw payload was not preserved")
	}
}

func TestPrepareReportIncludesNativeMultiplexerContext(t *testing.T) {
	t.Parallel()
	location := registry.Location{Kind: registry.MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"}
	prepared, err := prepareReport(nil, reportOptions{
		harness: "codex", sessionID: "session", event: "turn_complete",
	}, reportRuntimeContext{multiplexer: location, defaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.observation.Location() == nil || *prepared.observation.Location() != location {
		t.Fatalf("multiplexer context = %#v", prepared.observation.Location())
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
	if prepared.observation.Report().Lifecycle == nil || *prepared.observation.Report().Lifecycle != registry.NativeLifecycleResume {
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
			if observation.Report().Lifecycle == nil || *observation.Report().Lifecycle != test.lifecycle {
				t.Fatalf("lifecycle = %#v, want %q", observation.Report().Lifecycle, test.lifecycle)
			}
			if observation.Report().Claim == nil || *observation.Report().Claim != test.presence {
				t.Fatalf("presence = %#v, want %q", observation.Report().Claim, test.presence)
			}
			if observation.Report().Event != test.nativeEvent {
				t.Fatalf("native event = %q, want %q", observation.Report().Event, test.nativeEvent)
			}
		})
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
		{
			name:    "openclaw id",
			options: reportOptions{harness: "openclaw", event: "agent_end", sessionID: "openclaw-session"},
			want:    []string{"openclaw", "tui", "--session", "openclaw-session"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prepared, err := prepareReport(nil, test.options, reportRuntimeContext{defaultObservedAt: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if prepared.observation.Listing() == nil || !slices.Equal(prepared.observation.Listing().ResumeCommand, test.want) {
				t.Fatalf("resume command = %#v, want %#v", prepared.observation.Listing(), test.want)
			}
		})
	}
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
	if prepared.observation.ProcessIdentity() == nil || prepared.observation.ProcessIdentity().PID != agent.PID || prepared.observation.ProcessIdentity().StartIdentity != agent.StartIdentity {
		t.Fatalf("report process identity = %#v, want agent process %#v", prepared.observation.ProcessIdentity(), agent)
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
		harness: "codex", presence: "live", evidence: "process", pid: process.PID, event: "process.start", sessionID: "native-process-session",
	}, reportRuntimeContext{processes: []processinfo.Process{process}, defaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	observation := prepared.observation
	if observation.Kind() != "sighting" || observation.ActivityClaim() != nil || observation.Report().Event != "" {
		t.Fatalf("process observation retained native fields: %#v", observation)
	}
	if err := observation.Validate(catalog.Rules{}); err != nil {
		t.Fatalf("process observation is invalid: %v", err)
	}
}

func TestShimProcessReportsInferIdentityAndTransitionState(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/bin/sh", CWD: "/work", TTY: "/dev/pts/4"}
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
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
	store *registry.Journal,
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
	if session.Presence() != wantPresence {
		t.Fatalf("session presence = %q, want %q", session.Presence(), wantPresence)
	}
	return session.ID
}

func requireShimObservation(t *testing.T, observation registry.Observation, process processinfo.Process, present bool) {
	t.Helper()
	if observation.Kind() != "sighting" || observation.ProcessIdentity() == nil || !observation.ProcessIdentity().Complete() || observation.ProcessIdentity().StartIdentity != process.StartIdentity {
		t.Fatalf("shim process identity = %#v", observation.ProcessIdentity())
	}
	if observation.Present() == nil || *observation.Present() != present {
		t.Fatalf("process presence = %#v, want %v", observation.Present(), present)
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
	if prepared.observation.Subject.SessionID != "codex-image" || prepared.observation.Report().Event != "PostToolUse" {
		t.Fatalf("PostToolUse metadata = %#v", prepared.observation)
	}
	if len(prepared.observation.Report().Payload) != 0 {
		t.Fatalf("PostToolUse raw payload was retained: %d bytes", len(prepared.observation.Report().Payload))
	}
}
