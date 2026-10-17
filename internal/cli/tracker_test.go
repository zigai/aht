package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/observer"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/internal/service"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestTrackerLifecycleCommandsUseHumanOutputUnlessJSONRequested(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(registry.StateDirEnv, filepath.Join(home, "state"))
	storePath := filepath.Join(home, "sessions.json")

	for _, args := range [][]string{{"manage", "tracker", "enable", "--dry-run"}, {"manage", "tracker", "status"}, {"manage", "tracker", "disable", "--dry-run"}} {
		var stdout bytes.Buffer
		if err := runTestCLI(context.Background(), append([]string{"--store", storePath}, args...), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.Contains(stdout.String(), "Manager:") {
			t.Fatalf("%v default output = %q", args, stdout.String())
		}
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "enable", "--dry-run"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result service.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Manager == "" {
		t.Fatalf("tracker JSON = %q, %v", stdout.String(), err)
	}
}

func TestTrackerRunOnceSupportsHumanAndJSONOutput(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	var human bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "manage", "tracker", "run", "--once"}, &human, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(human.String()), "{") || !strings.Contains(human.String(), "processes=") {
		t.Fatalf("tracker run human output = %q", human.String())
	}

	var machine bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "tracker", "run", "--once"}, &machine, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(machine.Bytes(), &result); err != nil {
		t.Fatalf("tracker run JSON = %q, %v", machine.String(), err)
	}
}

func TestTrackerRunOnceExpiresIdentifiedTombstones(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})
	now := time.Now().UTC()
	for index, goneAt := range []time.Time{now.Add(-time.Hour), now.Add(-time.Minute)} {
		process := registry.ProcessIdentity{PID: 900000 + index, StartIdentity: "boot:" + strconv.Itoa(index)}
		running := registry.ActivityRunning
		report := registry.Observation{Harness: registry.Harness("codex"), At: goneAt.Add(-time.Second), Subject: registry.ObservationIdentity{SessionID: "tombstone-" + strconv.Itoa(index)}, Evidence: &registry.Report{Event: "start", Activity: &running, Process: &process}}
		gone := registry.Observation{Harness: registry.Harness("codex"), At: goneAt, Evidence: &registry.Sighting{Process: process, Present: false}}
		if _, err := store.ObserveBatch(t.Context(), []registry.Observation{report, gone}); err != nil {
			t.Fatal(err)
		}
	}

	if err := runTestCLI(t.Context(), []string{"--store", storePath, "manage", "tracker", "run", "--once", "--quiet"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil && !errors.Is(err, errObserverRunDegraded) {
		t.Fatal(err)
	}
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// The cycle also records any real agents on the host; only the seeded
	// tombstones matter here.
	var tombstones []string
	for _, session := range sessions {
		if strings.HasPrefix(session.SessionID, "tombstone-") {
			tombstones = append(tombstones, session.SessionID+"/"+string(session.Presence()))
		}
	}
	if len(tombstones) != 1 || tombstones[0] != "tombstone-1/gone" {
		t.Fatalf("tombstones after --once = %v, want only tombstone-1/gone", tombstones)
	}
}

var errTestPaneList = errors.New("pane inventory unavailable")

func TestQuietLongRunningObserverStreamsRequestedJSONLines(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &application{outputJSON: true, stdout: &stdout, stderr: &stderr}
	watcher := observer.New(observer.Options{
		Store: registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			cancel()
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]observer.CatalogEntry, error) { return nil, nil },
	})
	if err := app.runObserver(ctx, observeOptions{interval: time.Second, quiet: true}, watcher); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSON line count = %d, want 1; output=%q", len(lines), stdout.String())
	}
	var result observer.Result
	if err := json.Unmarshal([]byte(lines[0]), &result); err != nil {
		t.Fatalf("expected compact JSON line: %v; output=%q", err, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("quiet observer wrote diagnostics: %q", stderr.String())
	}
}

func TestRunObserverOnceReturnsDegradedErrorAfterWritingResult(t *testing.T) {
	t.Parallel()
	for _, outputJSON := range []bool{false, true} {
		var stdout bytes.Buffer
		app := &application{outputJSON: outputJSON, stdout: &stdout, stderr: &bytes.Buffer{}}
		watcher := observer.New(observer.Options{
			Store:       registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
			ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
			PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, errTestPaneList },
			CatalogList: func(context.Context) ([]observer.CatalogEntry, error) { return nil, nil },
		})
		err := app.runObserver(context.Background(), observeOptions{once: true}, watcher)
		if !errors.Is(err, errObserverRunDegraded) {
			t.Fatalf("outputJSON=%t error = %v", outputJSON, err)
		}
		if outputJSON {
			var result observer.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Degraded || result.Error != errTestPaneList.Error() {
				t.Fatalf("JSON degraded result = %q, %#v, %v", stdout.String(), result, err)
			}
			continue
		}
		if !strings.Contains(stdout.String(), "degraded=true") || !strings.Contains(stdout.String(), errTestPaneList.Error()) {
			t.Fatalf("human degraded result = %q", stdout.String())
		}
	}
}

func TestHistoryRefreshCoalescesTransitionsWithoutBlockingObserver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := historyRefreshStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- watchHistoryRefresh(ctx, store, func(ctx context.Context) {
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}()
		requireHistoryRefresh(t, started, "tracker startup did not start a refresh")
		for index, activity := range []registry.Activity{registry.ActivityIdle, registry.ActivityRunning, registry.ActivityIdle} {
			observation := registry.Observation{Harness: registry.Harness("pi"), At: time.Now().Add(time.Duration(index) * time.Second), Subject: registry.ObservationIdentity{SessionID: "sample"}, Evidence: &registry.Report{Event: "turn", Claim: new(registry.PresenceLive), Activity: &activity}}
			if _, err := store.Observe(t.Context(), observation); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		watcher := observer.New(observer.Options{
			Store:       store,
			ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
			PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
			CatalogList: func(context.Context) ([]observer.CatalogEntry, error) { return nil, nil },
		})
		for range 3 {
			if _, err := watcher.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		release <- struct{}{}
		requireHistoryRefresh(t, started, "idle transition during refresh did not start a follow-up")
		release <- struct{}{}
		synctest.Wait()
		select {
		case <-started:
			t.Fatal("transition burst started more than one follow-up refresh")
		default:
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("indexer shutdown: %v", err)
		}
	})
}

func TestHistoryRefreshCancellationJoinsActiveWork(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		store := historyRefreshStore(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started, stopping, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- watchHistoryRefresh(ctx, store, func(ctx context.Context) {
				close(started)
				<-ctx.Done()
				close(stopping)
				<-release
				close(stopped)
			})
		}()
		requireHistoryRefresh(t, started, "tracker startup did not start a refresh")
		cancel()
		<-stopping
		synctest.Wait()
		select {
		case <-done:
			close(release)
			t.Fatal("indexer returned before active work stopped")
		default:
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		select {
		case <-stopped:
		default:
			t.Fatal("indexer returned before active work stopped")
		}
	})
}

func historyRefreshStore(t *testing.T) *registry.MemoryStore {
	t.Helper()
	store, err := registry.OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func requireHistoryRefresh(t *testing.T, started <-chan struct{}, failure string) {
	t.Helper()
	synctest.Wait()
	select {
	case <-started:
	default:
		t.Fatal(failure)
	}
}

func TestHistoryRefreshSignalsOnlyIdleAndGoneTransitions(t *testing.T) {
	t.Parallel()
	var previous map[string]historySessionState
	for _, step := range []struct {
		presence registry.Presence
		activity registry.Activity
		want     bool
	}{
		{registry.PresenceLive, registry.ActivityRunning, false},
		{registry.PresenceLive, registry.ActivityIdle, true},
		{registry.PresenceLive, registry.ActivityIdle, false},
		{registry.PresenceLive, registry.ActivityWaiting, false},
		{registry.PresenceLive, registry.ActivityIdle, true},
		{registry.PresenceGone, registry.ActivityUnknown, true},
		{registry.PresenceGone, registry.ActivityUnknown, false},
		{registry.PresenceLive, registry.ActivityIdle, true},
	} {
		sessions := []registry.Session{{ID: "sample", UpdatedAt: time.Now(), Liveness: registry.NewLiveness(step.presence, step.activity, nil)}}
		changed, current := historyTransitions(sessions, previous)
		if changed != step.want {
			t.Fatalf("%s/%s transition signaled=%t, want %t", step.presence, step.activity, changed, step.want)
		}
		previous = current
	}
}
