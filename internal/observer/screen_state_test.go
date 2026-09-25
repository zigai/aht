package observer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestObserverDetectsScreenStateForTargetAgents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		harness      registry.Harness
		command      string
		screen       string
		want         registry.Activity
		wantFallback bool
	}{
		{harness: registry.Harness("codex"), command: "codex", screen: "› next task\nContext 63% used", want: registry.ActivityIdle},
		{harness: registry.Harness("claude"), command: "claude", screen: "Do you want to proceed?", want: registry.ActivityWaiting},
		{harness: registry.Harness("opencode"), command: "opencode", screen: "Working · esc to interrupt", want: registry.ActivityRunning, wantFallback: true},
		{harness: registry.Harness("pi"), command: "pi", screen: "Type a message · Enter to send", want: registry.ActivityIdle, wantFallback: true},
		{harness: registry.Harness("omp"), command: "omp", screen: " ⠋ Working... (40s)", want: registry.ActivityRunning, wantFallback: true},
	}
	for index, test := range tests {
		t.Run(string(test.harness), func(t *testing.T) {
			t.Parallel()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
			process, pane := detectionProcessPane(index+100, test.command)
			options := detectionObserverOptions(store, process, pane, t.TempDir())
			options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
				return mux.ScreenSnapshot{Text: test.screen, Title: test.command}, nil
			}
			if _, err := New(options).RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			sessions, err := store.List(context.Background(), registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].Activity() == nil || *sessions[0].Activity() != test.want || sessions[0].Decision() == nil || sessions[0].Decision().Authority != "screen" {
				t.Fatalf("sessions = %#v, want one %s screen decision", sessions, test.want)
			}
			if test.wantFallback && sessions[0].Decision().FallbackReason != "integration_report_missing" {
				t.Fatalf("screen fallback reason = %q, want integration_report_missing", sessions[0].Decision().FallbackReason)
			}
		})
	}
}

func TestObserverDisablesScreenInspection(t *testing.T) {
	t.Parallel()
	for _, located := range []bool{true, false} {
		t.Run(fmt.Sprintf("located=%t", located), func(t *testing.T) {
			t.Parallel()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
			process, pane := detectionProcessPane(198, "codex")
			options := detectionObserverOptions(store, process, pane, t.TempDir())
			options.DisableScreenInspection = true
			options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
				t.Error("screen capture called with inspection disabled")
				return mux.ScreenSnapshot{}, nil
			}
			if !located {
				options.PaneList = func(context.Context) ([]mux.Pane, error) { return nil, nil }
			}
			if _, err := New(options).RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			sessions, err := store.List(t.Context(), registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].Observations.Screen != nil {
				t.Fatalf("sessions = %#v, want process without screen evidence", sessions)
			}
		})
	}
}

func TestObserverUsesBundledOmpFallbackWhenNativeIntegrationIsMissing(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(198, "omp")
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		text := "╰────────╯\n ~/Projects/config · Codex · GPT-5.6-Sol · medium 22.7%/1M" +
			strings.Repeat("\n ", 20)
		return mux.ScreenSnapshot{Text: text}, nil
	}
	if _, err := New(options).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 ||
		sessions[0].Activity() == nil ||
		*sessions[0].Activity() != registry.ActivityIdle ||
		sessions[0].Decision() == nil ||
		sessions[0].Decision().RuleID != "custom_input_prompt" {
		t.Fatalf("sessions = %#v, want bundled OMP footer idle decision", sessions)
	}
	if sessions[0].Decision().FallbackReason != "integration_report_missing" {
		t.Fatalf("fallback reason = %q, want integration_report_missing", sessions[0].Decision().FallbackReason)
	}
}

func TestObserverTracksOmpScreenStateTransitions(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(208, "omp")
	at := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	screen := ""
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.Now = func() time.Time { return at }
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: screen}, nil
	}
	observer := New(options)

	transitions := []struct {
		screen string
		want   registry.Activity
		rule   string
	}{
		{screen: " ⠋ Working... (40s)", want: registry.ActivityRunning, rule: "custom_working"},
		{screen: "Permission required: allow / deny", want: registry.ActivityWaiting, rule: "permission_prompt"},
		{screen: " ~/Projects/config · Codex · GPT-5.6-Sol · medium 22.7%/1M" + strings.Repeat("\n ", 20), want: registry.ActivityIdle, rule: "custom_input_prompt"},
	}
	for _, transition := range transitions {
		screen = transition.screen
		if _, err := observer.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		sessions, err := store.List(context.Background(), registry.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 ||
			sessions[0].Activity() == nil ||
			*sessions[0].Activity() != transition.want ||
			sessions[0].Decision() == nil ||
			sessions[0].Decision().RuleID != transition.rule {
			t.Fatalf("sessions = %#v, want %s via %s", sessions, transition.want, transition.rule)
		}
		at = at.Add(time.Second)
	}
}

func TestObserverRecordsUnknownWhenStaleIntegrationHasNoTmuxPane(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(197, "pi")
	identity := observerProcessIdentity(process)
	presence := registry.PresenceLive
	idle := registry.ActivityIdle
	now := time.Now().UTC()
	_, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: now.Add(-registry.IntegrationActivityLease - time.Second), Subject: registry.ObservationIdentity{SessionID: "stale-pi"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: "pi-extension"}, Event: "agent_settled", Claim: &presence, Activity: &idle, Process: identity}})
	if err != nil {
		t.Fatal(err)
	}
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.PaneList = func(context.Context) ([]mux.Pane, error) { return nil, nil }
	captureCalled := false
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		captureCalled = true
		return mux.ScreenSnapshot{}, nil
	}
	if _, err := New(options).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if captureCalled {
		t.Fatal("observer attempted tmux capture without a pane")
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityUnknown || sessions[0].Decision() == nil || sessions[0].Decision().Reason != "screen_not_in_supported_multiplexer" || sessions[0].Decision().FallbackReason != "integration_report_stale" {
		t.Fatalf("stale integration without pane state = %#v", sessions)
	}
}

func TestObserverPreservesKnownActivityWhenScreenCaptureFails(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(198, "codex")
	at := time.Date(2026, 8, 12, 5, 0, 0, 0, time.UTC)
	captureFails := false
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.Now = func() time.Time { return at }
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		if captureFails {
			return mux.ScreenSnapshot{}, context.Canceled
		}
		return mux.ScreenSnapshot{Text: "› next task\nContext 63% used", Title: "codex"}, nil
	}
	observer := New(options)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	captureFails = true
	at = at.Add(time.Minute)
	result, err := observer.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded {
		t.Fatalf("capture failure did not degrade observer: %#v", result)
	}
	if health := observer.Health(); !health.Degraded || health.LastEnumerationErrorCategory != "reconciliation" || !strings.Contains(health.LastEnumerationError, "capturing codex pane") {
		t.Fatalf("capture failure health = %#v", health)
	}
	assertIdleSince(t, store, at.Add(-time.Minute))

	captureFails = false
	at = at.Add(time.Minute)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIdleSince(t, store, at.Add(-2*time.Minute))
}

func TestObserverPreservesIdleSinceAcrossUnrecognizedScreenRedraw(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(199, "codex")
	at := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	screen := "› next task\nContext 63% used"
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.Now = func() time.Time { return at }
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: screen, Title: "codex"}, nil
	}
	observer := New(options)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	screen = ""
	at = at.Add(time.Minute)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIdleSince(t, store, at.Add(-time.Minute))

	screen = "› next task\nContext 63% used"
	at = at.Add(time.Minute)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIdleSince(t, store, at.Add(-2*time.Minute))
}

func TestObserverReportsDetectionManifestLoadFailure(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(configDir, "omp.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(298, "omp")
	options := detectionObserverOptions(store, process, pane, configDir)
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: "ordinary screen", Title: ""}, nil
	}
	result, err := New(options).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded || !strings.Contains(result.Error, errDetectionOverrideInvalid.Error()) {
		t.Fatalf("manifest load failure result = %#v, want degraded detection error", result)
	}
}

func TestObserverPreservesKnownActivityAcrossPaneEnumerationFailure(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(299, "codex")
	at := time.Date(2026, 8, 12, 4, 30, 0, 0, time.UTC)
	paneEnumerationFails := false
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.Now = func() time.Time { return at }
	options.PaneList = func(context.Context) ([]mux.Pane, error) {
		if paneEnumerationFails {
			return nil, context.DeadlineExceeded
		}
		return []mux.Pane{pane}, nil
	}
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: "› next task\nContext 63% used", Title: "codex"}, nil
	}
	observer := New(options)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	paneEnumerationFails = true
	at = at.Add(time.Minute)
	result, err := observer.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded {
		t.Fatalf("pane enumeration failure did not degrade observer: %#v", result)
	}
	assertIdleSince(t, store, at.Add(-time.Minute))

	paneEnumerationFails = false
	at = at.Add(time.Minute)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertIdleSince(t, store, at.Add(-2*time.Minute))
}

func assertIdleSince(t *testing.T, store Store, wantSince time.Time) {
	t.Helper()

	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle || !sessions[0].ActivityChangedAt.Equal(wantSince) {
		t.Fatalf("sessions = %#v, want idle since %s", sessions, wantSince)
	}
}

func TestObserverNeverPersistsRawTerminalContents(t *testing.T) {
	t.Parallel()
	const secret = "PRIVATE-COMMAND-ARGUMENT"
	path := filepath.Join(t.TempDir(), "state.json")
	store := registry.NewJournal(path, catalog.Rules{})
	process, pane := detectionProcessPane(199, "codex")
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: "Would you like to run the following command? " + secret, Title: secret}, nil
	}
	if _, err := New(options).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "Would you like to run") {
		t.Fatalf("registry persisted terminal contents: %s", data)
	}
}

func TestObserverScreenDetectionRecoversMissedPermissionTransition(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process, pane := detectionProcessPane(201, "codex")
	at := time.Now().UTC()
	screen := "Would you like to run the following command?"
	options := detectionObserverOptions(store, process, pane, t.TempDir())
	options.Now = func() time.Time { return at }
	options.ScreenCapture = func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
		return mux.ScreenSnapshot{Text: screen, Title: "codex"}, nil
	}
	observer := New(options)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	screen = "› continue\nContext 63% used"
	at = at.Add(time.Second)
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle || sessions[0].Decision() == nil || sessions[0].Decision().RuleID != "input_prompt" {
		t.Fatalf("missed transition was not corrected: %#v", sessions)
	}
}

func TestScreenFallbackCannotOverrideConcurrentCompleteIntegration(t *testing.T) {
	t.Parallel()
	for _, harness := range []registry.Harness{registry.Harness("pi"), registry.Harness("opencode")} {
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
			process, pane := detectionProcessPane(300+len(harness), string(harness))
			options := detectionObserverOptions(store, process, pane, t.TempDir())
			options.ScreenCapture = func(ctx context.Context, _ mux.Pane) (mux.ScreenSnapshot, error) {
				running := registry.ActivityRunning
				presence := registry.PresenceLive
				integration := "pi-extension"
				if harness == registry.Harness("opencode") {
					integration = "opencode-plugin"
				}
				identity := observerProcessIdentity(process)
				_, err := store.Observe(ctx, registry.Observation{Harness: harness, At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "active"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: integration}, Event: "agent_start", Claim: &presence, Activity: &running, Process: identity}})
				if err != nil {
					return mux.ScreenSnapshot{}, fmt.Errorf("record concurrent integration report: %w", err)
				}
				return mux.ScreenSnapshot{Text: "Type a message · Enter to send"}, nil
			}
			if _, err := New(options).RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			sessions, err := store.List(context.Background(), registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityRunning || sessions[0].Decision() == nil || sessions[0].Decision().Authority != "hook" {
				t.Fatalf("screen fallback overrode active integration: %#v", sessions)
			}
		})
	}
}

func detectionObserverOptions(store Store, process processinfo.Process, pane mux.Pane, configDir string) Options {
	return Options{
		Store:              store,
		ProcessList:        func(context.Context) ([]processinfo.Process, error) { return []processinfo.Process{process}, nil },
		PaneList:           func(context.Context) ([]mux.Pane, error) { return []mux.Pane{pane}, nil },
		CatalogList:        func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		DetectionConfigDir: configDir,
		HealthPath:         filepath.Join("", ""),
		Now:                func() time.Time { return time.Now().UTC() },
	}
}

func detectionProcessPane(pid int, command string) (processinfo.Process, mux.Pane) {
	process := processinfo.Process{PID: pid, PPID: 1, ProcessGroupID: pid, Foreground: true, StartIdentity: "boot:" + command, Executable: "/usr/bin/" + command, CWD: "/work", TTY: "/dev/pts/9", Args: []string{command}}
	tmux := registry.Location{Kind: registry.MultiplexerTmux, ServerID: "default", SessionID: "$1", SessionName: "agents", WindowID: "@1", WindowIndex: "1", WindowName: "agents", PaneID: "%9", PaneIndex: "1", PaneCurrentPath: "/work", PanePID: 10, PaneTTY: "/dev/pts/9"}
	return process, mux.Pane{Location: tmux, Command: command, CWD: "/work", Title: command}
}

func observerProcessIdentity(process processinfo.Process) *registry.ProcessIdentity {
	return &registry.ProcessIdentity{PID: process.PID, PPID: process.PPID, ProcessGroupID: process.ProcessGroupID, Foreground: process.Foreground, StartIdentity: process.StartIdentity, Executable: process.Executable, CWD: process.CWD, TTY: process.TTY}
}
