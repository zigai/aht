package observer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errConfirmationPanes = errors.New("pane inventory unavailable")

type confirmationFixture struct {
	mu        sync.Mutex
	at        time.Time
	processes []processinfo.Process
	catalog   []CatalogEntry
}

func (f *confirmationFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *confirmationFixture) advance(d time.Duration, processes ...processinfo.Process) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
	f.processes = processes
}

func (f *confirmationFixture) list(context.Context) ([]processinfo.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]processinfo.Process(nil), f.processes...), nil
}

func (f *confirmationFixture) listCatalog(context.Context) ([]CatalogEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]CatalogEntry(nil), f.catalog...), nil
}

// continuousCycle runs one cycle the way Run does, without its timer.
func continuousCycle(t *testing.T, watcher *Observer) Result {
	t.Helper()
	watcher.continuous = true
	result, err := watcher.runCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func newConfirmationObserver(store Store, fixture *confirmationFixture, confirmation time.Duration) *Observer {
	return New(Options{
		Store:               store,
		Now:                 fixture.now,
		ProcessList:         fixture.list,
		PaneList:            func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList:         fixture.listCatalog,
		ProcessConfirmation: confirmation,
	})
}

func agentProcess(pid int) processinfo.Process {
	return processinfo.Process{PID: pid, PPID: 1, ProcessGroupID: pid, StartIdentity: "boot:" + strconv.Itoa(pid), Executable: "/usr/bin/codex", CWD: "/work", TTY: "/dev/pts/1"}
}

func TestObserverNeverRecordsShortLivedUnconfirmedProcess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	short := agentProcess(4100)
	fixture := &confirmationFixture{at: time.Now().UTC(), processes: []processinfo.Process{short}}
	watcher := newConfirmationObserver(store, fixture, 3*time.Second)

	for range 8 {
		if result := continuousCycle(t, watcher); result.Present != 0 || result.Observations != 0 {
			t.Fatalf("unconfirmed process produced observations: %#v", result)
		}
		sessions, err := store.List(context.Background(), registry.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 0 {
			t.Fatalf("unconfirmed process listed: %#v", sessions)
		}
		fixture.advance(300*time.Millisecond, short)
	}
	fixture.advance(300 * time.Millisecond)
	for range 3 {
		continuousCycle(t, watcher)
		fixture.advance(300 * time.Millisecond)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 0 || len(watcher.candidates) != 0 {
		t.Fatalf("short-lived process persisted = %#v, pending = %#v", persisted, watcher.candidates)
	}
}

func TestObserverRecordsProcessAfterConfirmationPeriod(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	process := agentProcess(4200)
	fixture := &confirmationFixture{at: time.Now().UTC(), processes: []processinfo.Process{process}}
	watcher := newConfirmationObserver(store, fixture, time.Second)

	if result := continuousCycle(t, watcher); result.Present != 0 {
		t.Fatalf("first sighting recorded before confirmation: %#v", result)
	}
	fixture.advance(time.Second, process)
	if result := continuousCycle(t, watcher); result.Present != 1 {
		t.Fatalf("process not recorded after confirmation period: %#v", result)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Presence() != registry.PresenceLive {
		t.Fatalf("confirmed sessions = %#v, want one live session", sessions)
	}
}

func TestObserverConfirmsNativeAndCatalogProcessesImmediately(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "state.json"), catalog.Rules{})
	native := agentProcess(4300)
	listed := agentProcess(4301)
	fixture := &confirmationFixture{
		at:        time.Now().UTC(),
		processes: []processinfo.Process{native, listed},
		catalog:   []CatalogEntry{{Harness: "codex", SessionID: "catalog-session", ProcessPID: listed.PID, Current: true}},
	}
	seedHookCreatedLiveSessionFor(t, store, fixture.at.Add(-time.Second), native)
	watcher := newConfirmationObserver(store, fixture, time.Hour)

	if result := continuousCycle(t, watcher); result.Present != 2 {
		t.Fatalf("native and catalog processes not confirmed at once: %#v", result)
	}
}

func seedHookCreatedLiveSessionFor(t *testing.T, store registry.Store, at time.Time, process processinfo.Process) {
	t.Helper()
	running := registry.ActivityRunning
	identity := registry.ProcessIdentity{PID: process.PID, PPID: process.PPID, ProcessGroupID: process.ProcessGroupID, StartIdentity: process.StartIdentity, Executable: process.Executable}
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: "codex", At: at, Subject: registry.ObservationIdentity{SessionID: "native-session"}, Evidence: &registry.Report{Event: "agent_start", Activity: &running, Process: &identity}}); err != nil {
		t.Fatal(err)
	}
}

type fileIdentity struct {
	inode   uint64
	modTime time.Time
	size    int64
}

func statIdentity(t *testing.T, path string) fileIdentity {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileIdentity{}
	}
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("file inode unavailable on this platform")
	}
	return fileIdentity{inode: stat.Ino, modTime: info.ModTime(), size: info.Size()}
}

func waitForScreenIdleSession(t *testing.T, store Store, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		sessions, err := store.List(context.Background(), registry.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		idle := false
		for _, session := range sessions {
			idle = idle || (session.Activity() != nil && *session.Activity() == registry.ActivityIdle)
		}
		if len(sessions) == count && idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tracker did not settle: %#v", sessions)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestTrackerSteadyStateDoesNotRewriteState runs the realtime tracker pieces at
// the default 300 ms interval with unchanged live sessions and fails if
// heartbeat cycles rewrite the snapshot, append the journal, or rewrite the
// observer health file.
func TestTrackerSteadyStateDoesNotRewriteState(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("runs the tracker for several seconds")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	plain := processinfo.Process{PID: 4400, PPID: 1, ProcessGroupID: 4400, Foreground: true, StartIdentity: "boot:4400", Executable: "/usr/bin/codex", CWD: "/plain", TTY: "/dev/pts/2"}
	paned := processinfo.Process{
		PID: 4401, PPID: 1, ProcessGroupID: 4401, Foreground: true, StartIdentity: "boot:4401",
		Executable: "/usr/bin/codex", CWD: "/repo", MultiplexerKind: "zellij", MultiplexerSession: "work", MultiplexerPane: "7",
	}
	pane := mux.Pane{
		Location: registry.Location{Kind: registry.MultiplexerZellij, SessionName: "work", TabID: "3", TabName: "agents", PaneID: "terminal_7", PaneCurrentPath: "/repo"},
		Command:  "codex", CWD: "/repo", Title: "Codex",
	}
	watcher := New(Options{
		Store:     store,
		StorePath: path,
		Interval:  300 * time.Millisecond,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{plain, paned}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) { return []mux.Pane{pane}, nil },
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› next task\nContext 63% used", Title: "Codex"}, nil
		},
		CatalogList:         func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		ProcessConfirmation: 600 * time.Millisecond,
		Quiet:               true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var group sync.WaitGroup
	var observeErr, persistErr error
	group.Go(func() { observeErr = watcher.Run(ctx) })
	group.Go(func() { persistErr = store.RunPersistence(ctx, 0, 0) })
	t.Cleanup(func() {
		cancel()
		group.Wait()
		if observeErr != nil || persistErr != nil {
			t.Errorf("tracker errors: observer %v, persistence %v", observeErr, persistErr)
		}
	})

	// Let both sessions be confirmed, created, and reach a stable screen state.
	waitForScreenIdleSession(t, store, 2)
	time.Sleep(time.Second)
	snapshot := statIdentity(t, path)
	journal := statIdentity(t, path+".journal.jsonl")
	health := statIdentity(t, path+".observer-health.json")
	before, err := store.State(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	cyclesBefore := watcher.Health().Cycles

	time.Sleep(3 * time.Second)

	if cycles := watcher.Health().Cycles - cyclesBefore; cycles < 8 {
		t.Fatalf("tracker ran %d cycles in 3s, want at least 8", cycles)
	}
	for file, before := range map[string]fileIdentity{path: snapshot, path + ".journal.jsonl": journal, path + ".observer-health.json": health} {
		if after := statIdentity(t, file); after != before {
			t.Fatalf("steady-state cycles wrote %s: before %+v after %+v", filepath.Base(file), before, after)
		}
	}
	if journal.size != 0 {
		t.Fatalf("journal holds %d bytes after checkpoint, want 0", journal.size)
	}
	after, err := store.State(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("steady-state heartbeats advanced revision %d -> %d", before.Revision, after.Revision)
	}
}

func TestObserverHealthFileWrittenOnStatusChangeOrSlowInterval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	healthPath := filepath.Join(dir, "health.json")
	at := time.Now().UTC()
	paneErr := errConfirmationPanes
	failPanes := true
	watcher := New(Options{
		Store:       registry.NewJournal(filepath.Join(dir, "state.json"), catalog.Rules{}),
		HealthPath:  healthPath,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
		PaneList: func(context.Context) ([]mux.Pane, error) {
			if failPanes {
				return nil, paneErr
			}
			return nil, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	cycle := func(advance time.Duration) fileIdentity {
		t.Helper()
		at = at.Add(advance)
		_, _ = watcher.RunOnce(context.Background())
		return statIdentity(t, healthPath)
	}

	first := cycle(0)
	if first.inode == 0 {
		t.Fatal("first cycle did not write observer health")
	}
	// Persistently degraded cycles in the same category do not rewrite it.
	for range 5 {
		if next := cycle(300 * time.Millisecond); next != first {
			t.Fatalf("degraded cycle rewrote health: before %+v after %+v", first, next)
		}
	}
	// Recovery is a status change and is written at once.
	failPanes = false
	recovered := cycle(300 * time.Millisecond)
	if recovered == first || !watcher.Health().LastSuccessAt.Equal(at) {
		t.Fatalf("recovery was not written: before %+v after %+v", first, recovered)
	}
	if next := cycle(300 * time.Millisecond); next != recovered {
		t.Fatalf("healthy cycle rewrote health: before %+v after %+v", recovered, next)
	}
	// The slow interval refreshes an unchanged status.
	if next := cycle(healthWriteInterval); next == recovered {
		t.Fatal("health was not refreshed after the slow interval")
	}
}
