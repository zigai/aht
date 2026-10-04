package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errTestWatcherExitedEarly = errors.New("test watcher exited early with nil error")
	errTestWatcherTimeout     = errors.New("timed out waiting for watcher readiness")
	errTestStartupFailed      = errors.New("startup failed")
)

func TestDiffWatchEventsSeparatesPresenceAndActivity(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	oldActivity := registry.ActivityIdle
	newActivity := registry.ActivityWaiting
	old := registry.Session{
		ID:        "s",
		Harness:   registry.Harness("codex"),
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&oldActivity), nil),
	}
	next := old
	next.Liveness = registry.NewLiveness(next.Presence(), registry.ActivityValue(&newActivity), next.Decision())
	next.ActivityChangedAt = at.Add(time.Minute)
	next.UpdatedAt = at.Add(time.Minute)
	events := diffWatchEvents(map[string]registry.Session{"s": old}, map[string]registry.Session{"s": next}, at.Add(2*time.Minute))
	if len(events) != 1 || events[0].Action != watchActionActivityChanged {
		t.Fatalf("unexpected activity events: %#v", events)
	}
	if events[0].PreviousActivity == nil || *events[0].PreviousActivity != registry.ActivityIdle {
		t.Fatalf("missing previous activity: %#v", events[0])
	}
}

func TestDiffWatchEventsReportsMultiplexerLocationChanges(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	activity := registry.ActivityIdle
	old := registry.Session{
		ID:        "s",
		Harness:   registry.Harness("codex"),
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&activity), nil),
	}
	next := old
	next.Location = registry.Location{Kind: registry.MultiplexerZellij, SessionName: "work", PaneID: "terminal_7"}
	next.UpdatedAt = at.Add(time.Second)
	events := diffWatchEvents(map[string]registry.Session{"s": old}, map[string]registry.Session{"s": next}, at.Add(2*time.Second))
	if len(events) != 1 || events[0].Action != watchActionLocationChanged || events[0].Location != "zellij:work:terminal_7" {
		t.Fatalf("multiplexer location events = %#v", events)
	}
}

func TestDiffWatchEventsIgnoresTransientWindowNameAndPathChanges(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	activity := registry.ActivityIdle
	old := registry.Session{
		ID:      "s",
		Harness: registry.Harness("omp"),
		Location: registry.Location{
			Kind:            registry.MultiplexerTmux,
			SessionName:     "0",
			WindowIndex:     "2",
			WindowName:      "zsh",
			PaneID:          "%1",
			PaneCurrentPath: "/home/user/src/app",
		},
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&activity), nil),
	}
	// Only WindowName (fleeting command name) and PaneCurrentPath change
	next := old
	next.Location.WindowName = "git"
	next.Location.PaneCurrentPath = "/home/user/src/app/internal"
	next.UpdatedAt = at.Add(time.Second)

	events := diffWatchEvents(map[string]registry.Session{"s": old}, map[string]registry.Session{"s": next}, at.Add(2*time.Second))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for transient window name/path change, got %#v", events)
	}
}

func TestDiffWatchEventsIgnoresTransientMultiplexerTabNameChanges(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	activity := registry.ActivityIdle
	old := registry.Session{
		ID:      "s2",
		Harness: registry.Harness("claude"),
		Location: registry.Location{
			Kind:        registry.MultiplexerZellij,
			SessionName: "main",
			TabID:       "1",
			TabName:     "default",
			PaneID:      "terminal_1",
		},
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&activity), nil),
	}
	next := old
	next.Location.TabName = "running-editor"
	next.UpdatedAt = at.Add(time.Second)

	events := diffWatchEvents(map[string]registry.Session{"s2": old}, map[string]registry.Session{"s2": next}, at.Add(2*time.Second))
	if len(events) != 0 {
		t.Fatalf("expected 0 events for transient multiplexer tab name change, got %#v", events)
	}
}

type testWatchHandle struct {
	cancel context.CancelFunc
	done   <-chan error
	joined <-chan struct{}
}

func waitTestWatchReady(ready <-chan struct{}, done <-chan error, timeout time.Duration) error {
	select {
	case <-ready:
		return nil
	case err := <-done:
		if err == nil {
			return errTestWatcherExitedEarly
		}
		return fmt.Errorf("test watcher exited early: %w", err)
	case <-time.After(timeout):
		return errTestWatcherTimeout
	}
}

func startTestWatch(t *testing.T, run func(ctx context.Context, ready chan struct{}) error) *testWatchHandle {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan struct{})
	done := make(chan error, 1)
	joined := make(chan struct{})

	go func() {
		defer close(joined)
		done <- run(ctx, ready)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Errorf("test watcher failed to stop within deadline")
		}
	})

	if err := waitTestWatchReady(ready, done, 5*time.Second); err != nil {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Errorf("test watcher failed to stop within deadline after startup failure")
		}
		t.Fatalf("readiness wait: %v", err)
	}

	return &testWatchHandle{
		cancel: cancel,
		done:   done,
		joined: joined,
	}
}

func (h *testWatchHandle) stop(t *testing.T) {
	t.Helper()
	h.cancel()
	select {
	case <-h.joined:
	case <-time.After(5 * time.Second):
		t.Fatalf("test watcher failed to stop within deadline")
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("test watcher failed: %v", err)
		}
	default:
	}
}

func TestWaitTestWatchReadyObservesStartupError(t *testing.T) {
	t.Parallel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	done <- errTestStartupFailed
	err := waitTestWatchReady(ready, done, time.Second)
	if err == nil || !errors.Is(err, errTestStartupFailed) {
		t.Fatalf("waitTestWatchReady() = %v, want error wrapping %v", err, errTestStartupFailed)
	}
}

func TestWatchJSONModeEmitsJSONLinesOnlyWhenRequested(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	activity := registry.ActivityIdle
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now(), Subject: registry.ObservationIdentity{SessionID: "watch-json"}, Evidence: &registry.Report{Activity: &activity}}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &application{storePath: path, outputJSON: true, stdout: &stdout, stderr: &bytes.Buffer{}}
	watcher := startTestWatch(t, func(ctx context.Context, ready chan struct{}) error {
		options, err := app.prepareWatch(watchOptions{ready: ready})
		if err != nil {
			return err
		}
		return app.runWatch(ctx, options)
	})
	watcher.stop(t)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("watch JSONL lines = %d: %q", len(lines), stdout.String())
	}
	var event watchEvent
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil || event.Action != watchActionSnapshot {
		t.Fatalf("watch JSONL event = %q, %v", lines[0], err)
	}
}

func TestWatchDefaultsToHumanTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	activity := registry.ActivityIdle
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now(), Subject: registry.ObservationIdentity{SessionID: "watch-human"}, Evidence: &registry.Report{Activity: &activity}}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &application{storePath: path, stdout: &stdout, stderr: &bytes.Buffer{}}
	watcher := startTestWatch(t, func(ctx context.Context, ready chan struct{}) error {
		options, err := app.prepareWatch(watchOptions{ready: ready})
		if err != nil {
			return err
		}
		return app.runWatch(ctx, options)
	})
	watcher.stop(t)
	if !strings.Contains(stdout.String(), "Time") || !strings.Contains(stdout.String(), "Event") || !strings.Contains(stdout.String(), "snapshot") || !strings.Contains(stdout.String(), "watch-human") || strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("watch default output = %q", stdout.String())
	}
}

func TestWatchNoSnapshotSignalsReadyWithoutOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	activity := registry.ActivityIdle
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now(), Subject: registry.ObservationIdentity{SessionID: "watch-no-snapshot"}, Evidence: &registry.Report{Activity: &activity}}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &application{storePath: path, stdout: &stdout, stderr: &bytes.Buffer{}}
	watcher := startTestWatch(t, func(ctx context.Context, ready chan struct{}) error {
		options, err := app.prepareWatch(watchOptions{noSnapshot: true, ready: ready})
		if err != nil {
			return err
		}
		return app.runWatch(ctx, options)
	})
	watcher.stop(t)
	if stdout.Len() != 0 {
		t.Fatalf("--no-snapshot output = %q", stdout.String())
	}
}

func TestDiffWatchEventsReportsProcessTransitions(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	activity := registry.ActivityUnknown
	old := registry.Session{
		ID:        "s",
		Harness:   registry.Harness("claude"),
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceUnknown, registry.ActivityValue(&activity), nil),
	}
	next := old
	next.Liveness = registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(next.Activity()), next.Decision())
	next.PresenceChangedAt = at.Add(time.Second)
	next.UpdatedAt = at.Add(time.Second)
	next.Process = &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	events := diffWatchEvents(map[string]registry.Session{"s": old}, map[string]registry.Session{"s": next}, at.Add(2*time.Second))
	if len(events) != 2 || events[0].Action != watchActionPresenceChanged || events[1].Action != watchActionProcessBound {
		t.Fatalf("unexpected process bind events: %#v", events)
	}
}

func TestDiffWatchEventsReportsProcessBindingWithoutPresenceChange(t *testing.T) {
	t.Parallel()

	at := time.Now().UTC()
	activity := registry.ActivityIdle
	old := registry.Session{
		ID:        "s",
		Harness:   registry.Harness("codex"),
		UpdatedAt: at,
		Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(&activity), nil),
	}
	next := old
	next.Process = &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	next.UpdatedAt = at.Add(time.Second)
	events := diffWatchEvents(
		map[string]registry.Session{"s": old},
		map[string]registry.Session{"s": next},
		at.Add(2*time.Second),
	)
	if len(events) != 1 || events[0].Action != watchActionProcessBound {
		t.Fatalf("live-to-live process binding events = %#v", events)
	}
}

func TestFormatWatchPlainUsesNullableActivity(t *testing.T) {
	t.Parallel()
	event := watchEvent{Time: time.Unix(0, 0), Action: watchActionRemoved, Harness: registry.Harness("codex"), Presence: registry.PresenceGone, Label: "gone"}
	want := "1970-01-01T00:00:00Z removed codex gone null session=gone"
	if got := formatWatchPlainEvent(event); got != want {
		t.Fatalf("formatWatchPlainEvent(nil activity) = %q, want %q", got, want)
	}
	unknown := registry.ActivityUnknown
	unknownEvent := watchEvent{Time: time.Unix(0, 0), Action: watchActionRemoved, Harness: registry.Harness("codex"), Presence: registry.PresenceGone, Activity: &unknown, Label: "gone"}
	wantUnknown := "1970-01-01T00:00:00Z removed codex gone unknown session=gone"
	if got := formatWatchPlainEvent(unknownEvent); got != wantUnknown {
		t.Fatalf("formatWatchPlainEvent(unknown activity) = %q, want %q", got, wantUnknown)
	}
}

func TestFormatEmptyWatchSnapshotIsExplicit(t *testing.T) {
	t.Parallel()
	event := watchEvent{Time: time.Unix(0, 0).UTC(), Action: watchActionSnapshotEmpty}
	for _, output := range []string{formatWatchPlainEvent(event), formatWatchTableEvent(event)} {
		if !strings.Contains(output, "snapshot_empty") || !strings.Contains(output, "no sessions") || strings.Contains(output, "null") || strings.Contains(output, "session=") {
			t.Fatalf("empty snapshot output = %q", output)
		}
		assertHumanLinesBounded(t, output)
	}
}
