package reporting

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

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPrepareReportCarriesIndependentDimensions(t *testing.T) {
	t.Parallel()

	prepared, err := Prepare(
		strings.NewReader(
			`{"session_id":"session-1","cwd":"/work","hook_event_name":"PermissionRequest","model":"gpt-5"}`,
		),
		Options{
			Harness:       "codex",
			Presence:      "live",
			Activity:      "waiting",
			SessionID:     "session-1",
			Event:         "permission_prompt",
			CWD:           "/work",
			ProjectRoot:   "/work",
			ResumeCommand: []string{"codex", "resume", "session-1"},
			RawStdin:      true,
		},
		Runtime{
			Location: registry.Location{
				Kind:        registry.MultiplexerTmux,
				SessionName: "dev",
				PaneID:      "%4",
			},
			DefaultObservedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if prepared.Observation.Report().Claim == nil ||
		*prepared.Observation.Report().Claim != registry.PresenceLive ||
		prepared.Observation.ActivityClaim() == nil ||
		*prepared.Observation.ActivityClaim() != registry.ActivityWaiting {
		t.Fatalf("independent dimensions lost: %#v", prepared.Observation)
	}

	if prepared.Observation.Listing() == nil ||
		len(prepared.Observation.Listing().ResumeCommand) != 3 {
		t.Fatalf("catalog metadata missing: %#v", prepared.Observation.Listing())
	}

	if prepared.Observation.Location() == nil ||
		prepared.Observation.Location().SessionName != "dev" ||
		prepared.Observation.Location().PaneID != "%4" {
		t.Fatalf("tmux context missing: %#v", prepared.Observation.Location())
	}

	if len(prepared.Observation.Report().Payload) == 0 {
		t.Fatal("raw payload was not preserved")
	}
}

func TestPrepareReportIncludesNativeMultiplexerContext(t *testing.T) {
	t.Parallel()

	location := registry.Location{
		Kind:        registry.MultiplexerZellij,
		SessionName: "work",
		PaneID:      "terminal_7",
	}

	prepared, err := Prepare(nil, Options{
		Harness: "codex", SessionID: "session", Event: "turn_complete",
	}, Runtime{Location: location, DefaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	if prepared.Observation.Location() == nil || *prepared.Observation.Location() != location {
		t.Fatalf("multiplexer context = %#v", prepared.Observation.Location())
	}
}

func TestPrepareReportCarriesNativeLifecycle(t *testing.T) {
	t.Parallel()

	prepared, err := Prepare(nil, Options{
		Harness: "openclaw", Lifecycle: "resume", Presence: "live", Activity: "idle",
		SessionID: "native-session", Event: "session_start",
	}, Runtime{DefaultObservedAt: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	if prepared.Observation.Report().Lifecycle == nil ||
		*prepared.Observation.Report().Lifecycle != registry.NativeLifecycleResume {
		t.Fatalf("native lifecycle missing: %#v", prepared.Observation)
	}
}

func TestPrepareReportInfersLifecycleFromNativeEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		options     Options
		payload     string
		lifecycle   registry.NativeLifecycle
		presence    registry.Presence
		nativeEvent string
	}{
		{
			name: "codex startup",
			options: Options{
				Harness: "codex", Activity: "idle", RawDefaultsOnly: true,
			},
			payload: `{"session_id":"codex-start","cwd":"/work",` +
				`"hook_event_name":"SessionStart","source":"startup",` +
				`"model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleStart,
			presence:    registry.PresenceLive,
			nativeEvent: "SessionStart",
		},
		{
			name: "codex resume",
			options: Options{
				Harness: "codex", Activity: "idle", RawDefaultsOnly: true,
			},
			payload: `{"session_id":"codex-resume","cwd":"/work",` +
				`"hook_event_name":"SessionStart","source":"resume",` +
				`"model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleResume,
			presence:    registry.PresenceLive,
			nativeEvent: "SessionStart",
		},
		{
			name: "codex end",
			options: Options{
				Harness: "codex", RawDefaultsOnly: true,
			},
			payload: `{"session_id":"codex-end","cwd":"/work",` +
				`"hook_event_name":"SessionEnd","reason":"other",` +
				`"model":"gpt-5"}`,
			lifecycle:   registry.NativeLifecycleEnd,
			presence:    registry.PresenceGone,
			nativeEvent: "SessionEnd",
		},
		{
			name: "pi resume",
			options: Options{
				Harness:     "pi",
				Activity:    "idle",
				Event:       "session_start",
				SessionPath: "/tmp/pi-session.json",
				Attributes:  []string{"pi_reason=resume"},
			},
			lifecycle:   registry.NativeLifecycleResume,
			presence:    registry.PresenceLive,
			nativeEvent: "session_start",
		},
		{
			name: "opencode deleted",
			options: Options{
				Harness: "opencode", Event: "session.deleted", SessionID: "open-session",
			},
			lifecycle:   registry.NativeLifecycleEnd,
			presence:    registry.PresenceGone,
			nativeEvent: "session.deleted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prepared, err := Prepare(
				strings.NewReader(test.payload),
				test.options,
				Runtime{DefaultObservedAt: time.Now().UTC()},
			)
			if err != nil {
				t.Fatal(err)
			}

			observation := prepared.Observation
			if observation.Report().Lifecycle == nil ||
				*observation.Report().Lifecycle != test.lifecycle {
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
		options Options
		want    []string
	}{
		{
			name:    "codex id",
			options: Options{Harness: "codex", Event: "turn", SessionID: "codex-session"},
			want:    []string{"codex", "resume", "codex-session"},
		},
		{
			name: "pi path",
			options: Options{
				Harness:     "pi",
				Event:       "agent_settled",
				SessionPath: "/tmp/pi-session.json",
			},
			want: []string{"pi", "--session", "/tmp/pi-session.json"},
		},
		{
			name: "openclaw id",
			options: Options{
				Harness:   "openclaw",
				Event:     "agent_end",
				SessionID: "openclaw-session",
			},
			want: []string{"openclaw", "tui", "--session", "openclaw-session"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prepared, err := Prepare(
				nil,
				test.options,
				Runtime{DefaultObservedAt: time.Now().UTC()},
			)
			if err != nil {
				t.Fatal(err)
			}

			if prepared.Observation.Listing() == nil ||
				!slices.Equal(prepared.Observation.Listing().ResumeCommand, test.want) {
				t.Fatalf(
					"resume command = %#v, want %#v",
					prepared.Observation.Listing(),
					test.want,
				)
			}
		})
	}
}

func TestPrepareReportAttachesTrackedAgentProcess(t *testing.T) {
	t.Parallel()

	shell := processinfo.Process{
		PID:           50,
		PPID:          42,
		StartIdentity: "boot:50",
		Executable:    "/bin/sh",
		Args:          []string{"sh"},
	}
	wrapped := processinfo.Process{
		PID:            42,
		PPID:           10,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/node",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
		Args:           []string{"pi"},
	}
	server := processinfo.Process{
		PID:            42,
		PPID:           30,
		ProcessGroupID: 30,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/pi",
		CWD:            "/work",
	}
	client := processinfo.Process{
		PID:            30,
		PPID:           20,
		ProcessGroupID: 30,
		StartIdentity:  "boot:30",
		Executable:     "/usr/bin/pi",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
	}
	runner := processinfo.Process{
		PID:           20,
		PPID:          15,
		StartIdentity: "boot:20",
		Executable:    "/bin/sh",
		Args:          []string{"sh", "-c", "go test ./..."},
	}
	named := processinfo.Process{
		PID:           15,
		PPID:          1,
		StartIdentity: "boot:15",
		Executable:    "/usr/bin/make",
		Args:          []string{"make", "check", "pi"},
	}
	launcher := processinfo.Process{
		PID:            30,
		PPID:           10,
		ProcessGroupID: 30,
		StartIdentity:  "boot:30",
		Executable:     "/usr/bin/node",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
		Args:           []string{"node", "pi"},
	}

	tests := []struct {
		name      string
		ancestors []processinfo.Process
		want      processinfo.Process
	}{
		{name: "wrapped agent", ancestors: []processinfo.Process{shell, wrapped}, want: wrapped},
		{
			name:      "agent child of the same agent",
			ancestors: []processinfo.Process{shell, server, client},
			want:      client,
		},
		{
			name:      "agent launched by a wrapper",
			ancestors: []processinfo.Process{shell, server, launcher},
			want:      server,
		},
		{
			name:      "agent under an unrelated process naming the agent",
			ancestors: []processinfo.Process{shell, server, client, runner, named},
			want:      client,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prepared, err := Prepare(nil, Options{
				Harness: "pi", Activity: "running", SessionPath: "/tmp/session.json",
			}, Runtime{Processes: test.ancestors})
			if err != nil {
				t.Fatal(err)
			}

			got := prepared.Observation.ProcessIdentity()
			if got == nil || got.PID != test.want.PID ||
				got.StartIdentity != test.want.StartIdentity {
				t.Fatalf("report process identity = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPrepareReportProcessEvidenceRequiresCompleteIdentity(t *testing.T) {
	t.Parallel()

	_, err := Prepare(
		bytes.NewReader(nil),
		Options{Harness: "codex", Evidence: "process", SessionID: "session-1", PID: 12},
		Runtime{},
	)
	if err == nil {
		t.Fatal("expected incomplete process identity error")
	}
}

func TestPrepareReportProcessEvidenceDoesNotCarryNativeAuthority(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{
		PID:            42,
		PPID:           10,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/codex",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
	}

	prepared, err := Prepare(nil, Options{
		Harness:   "codex",
		Presence:  "live",
		Evidence:  "process",
		PID:       process.PID,
		Event:     "process.start",
		SessionID: "native-process-session",
	}, Runtime{Processes: []processinfo.Process{process}, DefaultObservedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	observation := prepared.Observation
	if observation.Kind() != registry.EvidenceSighting || observation.ActivityClaim() != nil ||
		observation.Report() != nil {
		t.Fatalf("process observation retained native fields: %#v", observation)
	}

	if err := observation.Validate(catalog.Rules{}); err != nil {
		t.Fatalf("process observation is invalid: %v", err)
	}
}

func TestShimProcessReportsInferIdentityAndTransitionState(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{
		PID:            42,
		PPID:           10,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/bin/sh",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
	}
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
	name string, presence string,
	present bool,
	observedAt time.Time,
	previousSessionID string,
) string {
	t.Helper()

	prepared, err := Prepare(nil, Options{
		Harness:  "droid",
		Presence: presence,
		Evidence: "process",
		PID:      process.PID,
		Event:    "process." + name,
	}, Runtime{Processes: []processinfo.Process{process}, DefaultObservedAt: observedAt})
	if err != nil {
		t.Fatal(err)
	}

	observation := prepared.Observation
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

func requireShimObservation(
	t *testing.T,
	observation registry.Observation,
	process processinfo.Process,
	present bool,
) {
	t.Helper()

	if observation.Kind() != registry.EvidenceSighting || observation.ProcessIdentity() == nil ||
		!observation.ProcessIdentity().Complete() ||
		observation.ProcessIdentity().StartIdentity != process.StartIdentity {
		t.Fatalf("shim process identity = %#v", observation.ProcessIdentity())
	}

	if observation.Present() == nil || *observation.Present() != present {
		t.Fatalf("process presence = %#v, want %v", observation.Present(), present)
	}
}

func TestPrepareReportRejectsConflictingStdinModes(t *testing.T) {
	t.Parallel()

	_, err := Prepare(
		strings.NewReader(`{}`),
		Options{Harness: "codex", RawStdin: true, RawDefaultsOnly: true},
		Runtime{},
	)
	if !errors.Is(err, errConflictingReportStdin) {
		t.Fatalf("stdin mode error = %v", err)
	}
}

func TestPrepareReportRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	_, err := Prepare(
		strings.NewReader(strings.Repeat("x", maxPayloadInputBytes+1)),
		Options{Harness: "codex", RawStdin: true},
		Runtime{},
	)
	if !errors.Is(err, errPayloadInputTooLarge) {
		t.Fatalf("error = %v, want %v", err, errPayloadInputTooLarge)
	}
}

func TestPrepareReportAcceptsLargeCodexPostToolUseDefaults(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"codex-image","transcript_path":null,"cwd":"/` +
		`work","hook_event_name":"PostToolUse","model":"gpt-5",` +
		`"tool_name":"view_image","tool_response":"` +
		strings.Repeat(
			"x",
			maxPayloadInputBytes,
		) + `"}`

	prepared, err := Prepare(
		strings.NewReader(payload),
		Options{Harness: "codex", Activity: "running", RawDefaultsOnly: true},
		Runtime{},
	)
	if err != nil {
		t.Fatal(err)
	}

	if prepared.Observation.Subject.SessionID != "codex-image" ||
		prepared.Observation.Report().Event != "PostToolUse" {
		t.Fatalf("PostToolUse metadata = %#v", prepared.Observation)
	}

	if len(prepared.Observation.Report().Payload) != 0 {
		t.Fatalf(
			"PostToolUse raw payload was retained: %d bytes",
			len(prepared.Observation.Report().Payload),
		)
	}
}

func TestPrepareReportKeepsClaudeStopRunningWhileBackgroundWorkIsInFlight(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		payload string
		want    registry.Activity
	}{
		"background subagent": {
			payload: `{"session_id":"s","cwd":"/work","hook_event_name":"Stop",` +
				`"background_tasks":[{"id":"a1","type":"subagent",` +
				`"status":"running","agent_type":"Explore"}],` +
				`"session_crons":[]}`,
			want: registry.ActivityRunning,
		},
		"background shell command": {
			payload: `{"session_id":"s","cwd":"/work","hook_event_name":"Stop",` +
				`"background_tasks":[{"id":"b1","type":"shell",` +
				`"status":"running","command":"go test ./..."}],` +
				`"session_crons":[]}`,
			want: registry.ActivityRunning,
		},
		"monitor only": {
			payload: `{"session_id":"s","cwd":"/work","hook_event_name":"Stop",` +
				`"background_tasks":[{"id":"m1","type":"monitor",` +
				`"status":"running"}],"session_crons":[]}`,
			want: registry.ActivityIdle,
		},
		"nothing in flight": {
			payload: `{"session_id":"s","cwd":"/work","hook_event_name":"Stop",` +
				`"background_tasks":[],"session_crons":[]}`,
			want: registry.ActivityIdle,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			prepared, err := Prepare(strings.NewReader(test.payload), Options{
				Harness: "claude", Activity: "idle", Event: "Stop", RawStdin: true,
			}, Runtime{DefaultObservedAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)})
			if err != nil {
				t.Fatal(err)
			}

			if claim := prepared.Observation.ActivityClaim(); claim == nil || *claim != test.want {
				t.Fatalf("activity = %v, want %s", claim, test.want)
			}
		})
	}
}

func TestPrepareReportStoresClaudePostToolUseWithoutToolContent(t *testing.T) {
	t.Parallel()

	payload := `{"session_id":"claude-read","cwd":"/work",` +
		`"hook_event_name":"PostToolUse","tool_name":"Read",` +
		`"tool_input":{"file_path":"/work/big.log"},"tool_response":"` +
		strings.Repeat(
			"x",
			maxPayloadInputBytes,
		) + `"}`

	prepared, err := Prepare(
		strings.NewReader(payload),
		Options{Harness: "claude", Activity: "running", Event: "PostToolUse", RawStdin: true},
		Runtime{},
	)
	if err != nil {
		t.Fatal(err)
	}

	var stored map[string]json.RawMessage
	if err := json.Unmarshal(prepared.Observation.Report().Payload, &stored); err != nil {
		t.Fatalf("decoding stored payload: %v", err)
	}

	if string(stored["tool_name"]) != `"Read"` {
		t.Fatalf("hook metadata was not stored: %s", prepared.Observation.Report().Payload)
	}

	if _, ok := stored["tool_input"]; ok {
		t.Fatal("tool_input was stored")
	}

	if _, ok := stored["tool_response"]; ok {
		t.Fatal("tool_response was stored")
	}
}
