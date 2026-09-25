package observer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestResultStringIncludesDegradedStateAndError(t *testing.T) {
	t.Parallel()
	result := Result{Observations: 1, Sessions: 2, Processes: 3, Panes: 4, Degraded: true, Error: "pane inventory unavailable"}
	text := result.String()
	if !strings.Contains(text, "degraded=true") || !strings.Contains(text, `error="pane inventory unavailable"`) {
		t.Fatalf("result string = %q", text)
	}
}

var errFailGoneObservation = errors.New("fail gone observation once")

type failGoneOnceStore struct {
	Store

	failed bool
}

func (store *failGoneOnceStore) ObserveBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error) {
	if !store.failed {
		for _, observation := range observations {
			if observation.Present() != nil && !*observation.Present() {
				store.failed = true
				return nil, errFailGoneObservation
			}
		}
	}

	sessions, err := store.Store.ObserveBatch(ctx, observations)
	if err != nil {
		return nil, fmt.Errorf("delegate gone-observation store: %w", err)
	}

	return sessions, nil
}

type conflictObservationStore struct {
	Store

	conflictPID  int
	enabled      bool
	batchOnly    bool
	batchCalls   int
	observeCalls int
}

func (store *conflictObservationStore) Observe(ctx context.Context, observation registry.Observation) (registry.Session, error) {
	store.observeCalls++
	if !store.batchOnly && store.conflicts(observation) {
		return registry.Session{}, registry.ErrObservationConflict
	}

	session, err := store.Store.Observe(ctx, observation)
	if err != nil {
		return registry.Session{}, fmt.Errorf("delegate conflict observation: %w", err)
	}

	return session, nil
}

func (store *conflictObservationStore) ObserveBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error) {
	store.batchCalls++
	if slices.ContainsFunc(observations, store.conflicts) {
		return nil, registry.ErrObservationConflict
	}

	sessions, err := store.Store.ObserveBatch(ctx, observations)
	if err != nil {
		return nil, fmt.Errorf("delegate conflict observation batch: %w", err)
	}

	return sessions, nil
}

func (store *conflictObservationStore) conflicts(observation registry.Observation) bool {
	return store.enabled && observation.ProcessIdentity() != nil && observation.ProcessIdentity().PID == store.conflictPID
}

func requireOnlySessionPresence(t *testing.T, store Store, want registry.Presence) {
	t.Helper()
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != want {
		t.Fatalf("sessions = %#v, want one %s session", sessions, want)
	}
}

func assertDegradedResult(t *testing.T, result Result, err error, targetErr error, expectedSubstrings ...string) {
	t.Helper()
	if !errors.Is(err, targetErr) || !result.Degraded || !strings.Contains(result.Error, targetErr.Error()) {
		t.Fatalf("RunOnce() error = %v, result = %#v, want degraded result wrapping %v", err, result, targetErr)
	}
	for _, substr := range expectedSubstrings {
		if !strings.Contains(result.Error, substr) {
			t.Fatalf("result.Error = %q, want substring %q", result.Error, substr)
		}
	}
}

func assertDegradedHealth(t *testing.T, health Health, targetErr error, expectedSubstrings ...string) {
	t.Helper()
	if !health.Degraded || !strings.Contains(health.LastEnumerationError, targetErr.Error()) {
		t.Fatalf("watcher health = %#v, want degraded with error %v", health, targetErr)
	}
	for _, substr := range expectedSubstrings {
		if !strings.Contains(health.LastEnumerationError, substr) {
			t.Fatalf("health.LastEnumerationError = %q, want substring %q", health.LastEnumerationError, substr)
		}
	}
}

func assertGoneAndTrackedCounts(t *testing.T, result Result, wantGone int, watcher *Observer, wantTracked int) {
	t.Helper()
	if result.Gone != wantGone {
		t.Fatalf("result gone = %d, want %d (result = %#v)", result.Gone, wantGone, result)
	}
	if len(watcher.tracked) != wantTracked {
		t.Fatalf("tracked process count = %d, want %d (tracked = %#v)", len(watcher.tracked), wantTracked, watcher.tracked)
	}
}

func assertPartialSuccessCounters(t *testing.T, result Result, wantPresent int) {
	t.Helper()
	if result.Present != wantPresent {
		t.Fatalf("RunOnce() result = %#v, want %d processes observed", result, wantPresent)
	}
	if result.Observations == 0 || result.Sessions == 0 || result.Changed == 0 {
		t.Fatalf("partial-success counters = %#v, want persisted independent observation", result)
	}
}

//nolint:cyclop // lifecycle test covers the two-snapshot disappearance contract
func TestObserverDefaultMissingRequiresTwoSnapshots(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
	processes := []processinfo.Process{process}
	watcher := New(Options{StorePath: path, Now: func() time.Time { return at }, ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil }, PaneList: func(context.Context) ([]mux.Pane, error) { return nil, nil }, HealthPath: path + ".health"})
	first, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Present != 1 || first.Gone != 0 {
		t.Fatalf("first result: %#v", first)
	}
	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("present sessions: %v %#v", err, sessions)
	}
	session := sessions[0]
	if session.Presence() != registry.PresenceLive || session.Activity() == nil || *session.Activity() != registry.ActivityUnknown {
		t.Fatalf("present session: %#v", session)
	}
	processes = nil
	at = at.Add(time.Second)
	second, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Gone != 0 {
		t.Fatalf("one miss marked gone: %#v", second)
	}
	at = at.Add(time.Second)
	third, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.Gone != 1 {
		t.Fatalf("second miss did not mark gone: %#v", third)
	}
	session, err = registry.NewJournal(path, catalog.Rules{}).Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone || session.Activity() != nil {
		t.Fatalf("gone session: %#v", session)
	}
}

func TestObserverRetriesFailedGoneObservationAndEvictsTrackedProcess(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	baseStore := registry.NewJournal(path, catalog.Rules{})
	store := &failGoneOnceStore{Store: baseStore}
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
	processes := []processinfo.Process{process}
	watcher := New(Options{
		Store: store,
		Now:   func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processes, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	processes = nil
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Second)
	failed, err := watcher.RunOnce(context.Background())
	assertDegradedResult(t, failed, err, errFailGoneObservation, "recording observation batch")
	assertDegradedHealth(t, watcher.Health(), errFailGoneObservation, "recording observation batch")
	assertGoneAndTrackedCounts(t, failed, 1, watcher, 1)
	at = at.Add(time.Second)
	retried, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertGoneAndTrackedCounts(t, retried, 1, watcher, 0)
	requireOnlySessionPresence(t, baseStore, registry.PresenceGone)
}

func TestObserverConflictDoesNotBlockIndependentObservation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	baseStore := registry.NewJournal(path, catalog.Rules{})
	store := &conflictObservationStore{Store: baseStore, conflictPID: 1234, enabled: true}
	at := time.Now().UTC().Add(-time.Minute)
	processes := []processinfo.Process{
		{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/conflict", TTY: "/dev/pts/1"},
		{PID: 5678, PPID: 1, ProcessGroupID: 5678, StartIdentity: "boot:B", Executable: "/usr/bin/codex", CWD: "/valid", TTY: "/dev/pts/2"},
	}
	watcher := New(Options{
		Store:       store,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})

	result, err := watcher.RunOnce(context.Background())
	assertDegradedResult(t, result, err, registry.ErrObservationConflict)
	assertDegradedHealth(t, watcher.Health(), registry.ErrObservationConflict)
	assertPartialSuccessCounters(t, result, 2)
	if store.batchCalls != 1 || store.observeCalls == 0 {
		t.Fatalf("store calls = batch %d, individual %d; want one atomic attempt followed by individual retries", store.batchCalls, store.observeCalls)
	}
	sessions, listErr := baseStore.List(context.Background(), registry.Filter{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(sessions) != 1 || sessions[0].Process == nil || sessions[0].Process.PID != 5678 {
		t.Fatalf("sessions after conflicted cycle = %#v, want independent process persisted", sessions)
	}
}

func TestObserverRunWithResultsDeliversDegradedResultOnStoreFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	baseStore := registry.NewJournal(path, catalog.Rules{})
	store := &failGoneOnceStore{Store: baseStore}
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
	processes := []processinfo.Process{process}
	watcher := New(Options{
		Store: store,
		Now:   func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processes, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	processes = nil
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Second)

	var streamed []Result
	ctx, cancel := context.WithCancel(context.Background())
	handle := func(r Result) error {
		streamed = append(streamed, r)
		cancel()
		return nil
	}
	_ = watcher.RunWithResults(ctx, handle)
	if len(streamed) != 1 {
		t.Fatalf("streamed results = %d, want 1", len(streamed))
	}
	r := streamed[0]
	if !r.Degraded || !strings.Contains(r.Error, errFailGoneObservation.Error()) || !strings.Contains(r.Error, "recording observation batch") || r.Gone != 1 {
		t.Fatalf("streamed result = %#v, want degraded with error and gone counter", r)
	}
	health := watcher.Health()
	if !health.Degraded || !strings.Contains(health.LastEnumerationError, errFailGoneObservation.Error()) || !strings.Contains(health.LastEnumerationError, "recording observation batch") {
		t.Fatalf("watcher health = %#v, want degraded with observation error", health)
	}
}

func TestObserverPreservesAtomicConflictAfterSuccessfulIndividualRetries(t *testing.T) {
	t.Parallel()

	store := &conflictObservationStore{
		Store:       registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
		conflictPID: 1234, enabled: true, batchOnly: true,
	}
	watcher := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{{
				PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A",
				Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1",
			}}, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})

	result, err := watcher.RunOnce(context.Background())
	if !errors.Is(err, registry.ErrObservationConflict) {
		t.Fatalf("RunOnce() error = %v, want original atomic conflict", err)
	}
	if result.Sessions == 0 || store.observeCalls == 0 {
		t.Fatalf("successful individual retries were not recorded: result=%#v calls=%d", result, store.observeCalls)
	}
}

func TestObserverTracksProcessesCommittedDuringConflictRecovery(t *testing.T) {
	t.Parallel()

	baseStore := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	store := &conflictObservationStore{Store: baseStore, conflictPID: 1234, enabled: true}
	at := time.Now().UTC().Add(-time.Minute)
	processes := []processinfo.Process{
		{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/conflict", TTY: "/dev/pts/1"},
		{PID: 5678, PPID: 1, ProcessGroupID: 5678, StartIdentity: "boot:B", Executable: "/usr/bin/codex", CWD: "/valid", TTY: "/dev/pts/2"},
	}
	watcher := New(Options{
		Store:       store,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); !errors.Is(err, registry.ErrObservationConflict) {
		t.Fatalf("initial cycle error = %v, want observation conflict", err)
	}

	store.enabled = false
	processes = nil
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	sessions, err := baseStore.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.Process != nil && session.Process.PID == 5678 && session.Presence() == registry.PresenceGone {
			return
		}
	}
	t.Fatalf("independent process was not retired after disappearing: %#v", sessions)
}

func TestObserverRetriesConflictingAbsenceWithoutAdvancingTracker(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sessions.json")
	baseStore := registry.NewJournal(path, catalog.Rules{})
	store := &conflictObservationStore{Store: baseStore, conflictPID: 1234}
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
	processes := []processinfo.Process{process}
	watcher := New(Options{
		Store:       store,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	processes = nil
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	store.enabled = true
	at = at.Add(time.Second)
	failed, err := watcher.RunOnce(context.Background())
	if !errors.Is(err, registry.ErrObservationConflict) || failed.Gone != 1 {
		t.Fatalf("conflicting absence cycle = %#v, err=%v", failed, err)
	}
	if len(watcher.tracked) != 1 {
		t.Fatalf("conflicting absence advanced tracker: %#v", watcher.tracked)
	}
	requireOnlySessionPresence(t, baseStore, registry.PresenceLive)

	store.enabled = false
	at = at.Add(time.Second)
	retried, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retried.Gone != 1 || len(watcher.tracked) != 0 {
		t.Fatalf("successful absence retry = %#v, tracked=%#v", retried, watcher.tracked)
	}
	requireOnlySessionPresence(t, baseStore, registry.PresenceGone)
}

func TestObserverRejectsConcurrentRunsOnOneInstance(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	watcher := New(Options{
		Store: registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			close(entered)
			<-release
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := watcher.RunOnce(context.Background())
		firstDone <- err
	}()
	<-entered
	if _, err := watcher.RunOnce(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("concurrent RunOnce() error = %v, want ErrAlreadyRunning", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
}

func TestObserverRetriesHealthWriteAfterPersistenceFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blockedParent := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedParent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	healthPath := filepath.Join(blockedParent, "health.json")
	at := time.Now().UTC()
	watcher := New(Options{
		Store:       registry.NewJournal(filepath.Join(root, "sessions.json"), catalog.Rules{}),
		HealthPath:  healthPath,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); err == nil {
		t.Fatal("expected health persistence failure")
	}
	if err := os.Remove(blockedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blockedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Second)
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatalf("health write retry error = %v", err)
	}
	if _, err := os.Stat(healthPath); err != nil {
		t.Fatalf("health file was not retried: %v", err)
	}
}

func TestObserverRestartMarksMissingStoredProcessGone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 1234, PPID: 1, ProcessGroupID: 1234, StartIdentity: "boot:A", Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
	processes := []processinfo.Process{process}
	options := Options{
		StorePath: path,
		Now:       func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processes, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		HealthPath:  path + ".health",
	}
	if _, err := New(options).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	processes = nil
	at = at.Add(time.Second)
	result, err := New(options).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Gone != 1 {
		t.Fatalf("restart result gone = %d, want 1: %#v", result.Gone, result)
	}

	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceGone || sessions[0].Activity() != nil || sessions[0].Decision() == nil || sessions[0].Decision().Reason != "process_gone" || sessions[0].Decision().Process.StartIdentity != process.StartIdentity {
		t.Fatalf("sessions after restart: %#v", sessions)
	}
}

func seedHookCreatedLiveSession(t *testing.T, path string, at time.Time, process processinfo.Process) {
	t.Helper()
	activity := registry.ActivityRunning
	presence := registry.PresenceLive
	_, err := registry.NewJournal(path, catalog.Rules{}).Observe(context.Background(), registry.Observation{Harness: registry.Harness("omp"), At: at, Subject: registry.ObservationIdentity{SessionID: "hook-only"}, Evidence: &registry.Report{Event: "agent_start", Claim: &presence, Activity: &activity, Process: &registry.ProcessIdentity{
		PID:           process.PID,
		StartIdentity: process.StartIdentity,
	}, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "0", PaneID: "%1"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestObserverMarksHookCreatedDeadProcessGoneInOneCycle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	seedHookCreatedLiveSession(t, path, at, processinfo.Process{PID: 4321, StartIdentity: "boot:Z"})

	options := Options{
		StorePath: path,
		Now:       func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		HealthPath:  path + ".health",
	}
	result, err := New(options).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Gone != 1 {
		t.Fatalf("result gone = %d, want 1: %#v", result.Gone, result)
	}

	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceGone {
		t.Fatalf("sessions after sweep: %#v", sessions)
	}
}

func TestObserverKeepsHookCreatedLiveProcessAndRetiresReusedPID(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	seedHookCreatedLiveSession(t, path, at, processinfo.Process{PID: 4321, StartIdentity: "boot:Z"})

	newOptions := func(now time.Time, processes []processinfo.Process) Options {
		return Options{
			StorePath: path,
			Now:       func() time.Time { return now },
			ProcessList: func(context.Context) ([]processinfo.Process, error) {
				return processes, nil
			},
			PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
			CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
			HealthPath:  path + ".health",
		}
	}
	liveProcesses := []processinfo.Process{{PID: 4321, StartIdentity: "boot:Z", Executable: "/usr/bin/omp"}}
	if _, err := New(newOptions(at, liveProcesses)).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceLive {
		t.Fatalf("matching live process must stay live: %#v", sessions)
	}

	reusedProcesses := []processinfo.Process{{PID: 4321, StartIdentity: "boot:NEW", Executable: "/usr/bin/omp"}}
	result, err := New(newOptions(at.Add(time.Second), reusedProcesses)).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Gone != 1 {
		t.Fatalf("reused pid result gone = %d, want 1: %#v", result.Gone, result)
	}
}

func TestObserverMarksHookCreatedSessionWithDeadPanePIDGone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	activity := registry.ActivityRunning
	presence := registry.PresenceLive
	_, err := registry.NewJournal(path, catalog.Rules{}).Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionID: "hook-pi-pane"}, Evidence: &registry.Report{Event: "agent_start", Claim: &presence, Activity: &activity, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "0", PaneID: "%1", PanePID: 1392}}})
	if err != nil {
		t.Fatal(err)
	}

	options := Options{
		StorePath: path,
		Now:       func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			// Process 1392 has died, only unrelated process exists
			return []processinfo.Process{{PID: 99999, StartIdentity: "boot:99999"}}, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		HealthPath:  path + ".health",
	}
	result, err := New(options).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Gone != 1 {
		t.Fatalf("result gone = %d, want 1: %#v", result.Gone, result)
	}

	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceGone {
		t.Fatalf("sessions after dead pane PID sweep: %#v", sessions)
	}
}

func TestObserverRetiresStaleSessionWithPreviousProcessObservation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)

	store := registry.NewJournal(path, catalog.Rules{})
	seedStaleSessionHistory(t, store, at)
	preSessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil || len(preSessions) != 1 || preSessions[0].Presence() != registry.PresenceLive || preSessions[0].Observations.Process == nil {
		t.Fatalf("precondition failed: sessions=%#v, error=%v", preSessions, err)
	}

	// Run observer with no live processes
	at = at.Add(10 * time.Second)
	options := Options{
		StorePath: path,
		Now:       func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		HealthPath:  path + ".health",
	}

	result, err := New(options).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Gone != 1 {
		t.Fatalf("result gone = %d, want 1: %#v", result.Gone, result)
	}

	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceGone || sessions[0].Activity() != nil {
		t.Fatalf("session was not retired: %#v", sessions)
	}
}

func seedStaleSessionHistory(t *testing.T, store registry.Store, at time.Time) {
	t.Helper()
	activity := registry.ActivityInterrupted
	presence := registry.PresenceLive
	present := false
	process := processinfo.Process{PID: 2527379, StartIdentity: "boot:dead"}

	start := registry.NativeLifecycleStart
	// 1. Initial hook observation
	_, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("omp"), At: at, Subject: registry.ObservationIdentity{SessionID: "stale-session"}, Evidence: &registry.Report{Event: "agent_start", Lifecycle: &start, Claim: &presence, Activity: &activity, Process: &registry.ProcessIdentity{
		PID:           process.PID,
		StartIdentity: process.StartIdentity,
	}, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "0", PaneID: "%53"}}})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Prior process observation showing process is absent
	_, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("omp"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{SessionID: "stale-session"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{
		PID:           process.PID,
		StartIdentity: process.StartIdentity,
	}, Present: present}})
	if err != nil {
		t.Fatal(err)
	}

	// 3. A subsequent start hook revived presence to live
	_, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("omp"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{SessionID: "stale-session"}, Evidence: &registry.Report{Event: "agent_start", Lifecycle: &start, Claim: &presence, Activity: &activity}})
	if err != nil {
		t.Fatal(err)
	}

	// 4. Agent end hook with interrupted activity
	_, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("omp"), At: at.Add(3 * time.Second), Subject: registry.ObservationIdentity{SessionID: "stale-session"}, Evidence: &registry.Report{Event: "agent_end", Activity: &activity}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunWithResultsStreamsEveryCycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	results := make([]Result, 0, 1)
	watcher := New(Options{
		Store: registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{}),
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			cancel()
			return nil, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if err := watcher.RunWithResults(ctx, func(result Result) error {
		results = append(results, result)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("result count = %d, want 1", len(results))
	}
}
