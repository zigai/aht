package observer

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

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
