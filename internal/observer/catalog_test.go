package observer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errTestNativeCatalogCommand = errors.New("native command failed")

func TestDecodeCatalogArrayAndEnvelope(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`[{"harness":"claude","session_id":"one","current":true}]`,
		`{"sessions":[{"harness":"goose","session_id":"two","current":false}]}`,
	} {
		entries, err := decodeCatalog([]byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].SessionID == "" {
			t.Fatalf("unexpected entries: %#v", entries)
		}
	}
}

func TestDefaultCatalogListRejectsUnknownHarness(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"unknown","session_id":"bad"}]`)
	t.Setenv(catalogFileEnv, "")
	if _, err := DefaultCatalogList(context.Background()); err == nil {
		t.Fatal("expected unknown harness error")
	}
}

func TestDefaultCatalogListCanonicalizesHarnessAlias(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"claude-code","session_id":"alias"}]`)
	t.Setenv(catalogFileEnv, "")

	entries, err := defaultCatalogList(context.Background(), func(context.Context) ([]catalog.NativeCatalogEntry, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Harness != "claude" {
		t.Fatalf("canonical entries = %#v", entries)
	}
}

func TestObserverCycleAcceptsCanonicalizedCatalogAlias(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{"harness":"claude-code","session_id":"alias","current":true}]`)
	t.Setenv(catalogFileEnv, "")
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	watcher := New(Options{
		Store:       store,
		StorePath:   path,
		HealthPath:  path + ".health",
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return nil, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(ctx context.Context) ([]CatalogEntry, error) {
			return defaultCatalogList(ctx, func(context.Context) ([]catalog.NativeCatalogEntry, error) { return nil, nil })
		},
		Now: func() time.Time { return time.Now().UTC() },
	})
	result, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Degraded {
		t.Fatalf("catalog alias degraded observer cycle: %#v", result)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Harness != registry.Harness("claude") {
		t.Fatalf("catalog sessions = %#v", sessions)
	}
}

func TestDefaultCatalogListEmptyWithoutConfiguration(t *testing.T) {
	t.Setenv(catalogJSONEnv, "")
	t.Setenv(catalogFileEnv, "")
	entries, err := defaultCatalogList(context.Background(), func(context.Context) ([]catalog.NativeCatalogEntry, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if entries != nil {
		t.Fatalf("expected nil catalog, got %#v", entries)
	}
}

func TestDecodeCatalogRejectsNull(t *testing.T) {
	t.Parallel()

	if _, err := decodeCatalog([]byte("null")); !errors.Is(err, errCatalogSessionsType) {
		t.Fatalf("decodeCatalog() error = %v, want %v", err, errCatalogSessionsType)
	}
}

func TestDefaultCatalogListRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxCatalogBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(catalogJSONEnv, "")
	t.Setenv(catalogFileEnv, path)

	_, err := DefaultCatalogList(context.Background())
	if !errors.Is(err, errCatalogTooLarge) {
		t.Fatalf("DefaultCatalogList() error = %v, want %v", err, errCatalogTooLarge)
	}
}

func TestDefaultCatalogListRetainsConfiguredAndNativeEntriesOnProviderError(t *testing.T) {
	t.Setenv(catalogJSONEnv, `[{
		"harness":"claude-code","session_id":"configured"
	}]`)
	t.Setenv(catalogFileEnv, "")
	providerFailure := errTestNativeCatalogCommand
	entries, err := defaultCatalogList(t.Context(), func(context.Context) ([]catalog.NativeCatalogEntry, error) {
		return []catalog.NativeCatalogEntry{{
			Harness: "goose", Reporter: registry.Reporter{Integration: "goose-catalog", Version: 3, MultiSession: true},
			Session: harness.CatalogSession{SessionID: "native", Current: true, Activity: registry.ActivityRunning},
		}}, providerFailure
	})
	if !errors.Is(err, providerFailure) {
		t.Fatalf("defaultCatalogList() error = %v, want provider failure", err)
	}
	if len(entries) != 2 || entries[0].Harness != "claude" || entries[1].SessionID != "native" {
		t.Fatalf("composed entries = %#v", entries)
	}
}

func TestCatalogActivityReportRequiresCurrentSessionAndCompleteListedProcess(t *testing.T) {
	t.Parallel()
	processes := map[int]processinfo.Process{
		42: {PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/bin/goose", CWD: "/work"},
	}
	entry := CatalogEntry{
		Harness: "goose", SessionID: "native", ProcessPID: 42, Current: true,
		Activity: registry.ActivityRunning, Attributes: map[string]string{"native_title": "example"},
		Reporter: registry.Reporter{Integration: "goose-catalog", Version: 3, MultiSession: true},
	}
	report, ok := catalogActivityReport(entry, processes)
	assertCatalogActivityReport(t, report, ok)
	entry.Current = false
	if _, ok := catalogActivityReport(entry, processes); ok {
		t.Fatal("saved catalog session produced an activity report")
	}
	entry.Current = true
	entry.ProcessPID = 99
	if _, ok := catalogActivityReport(entry, processes); ok {
		t.Fatal("catalog session without listed process produced an activity report")
	}
	entry.ProcessPID = 42
	processes[42] = processinfo.Process{PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:99", Executable: "/bin/codex", CWD: "/work"}
	if _, ok := catalogActivityReport(entry, processes); ok {
		t.Fatal("reused PID owned by another harness produced an activity report")
	}
	other := processes[42]
	other.AgentHint = "goose"
	processes[42] = other
	if _, ok := catalogActivityReport(entry, processes); ok {
		t.Fatal("conflicting AgentHint overrode the executable's harness")
	}
}

func TestPrimeCatalogRequiresMatchingWorkerAtListedPID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	worker := processinfo.Process{
		PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/prime-agent",
		Args: []string{"prime-agent", "worker"}, CWD: "/work",
	}
	for _, test := range []struct {
		name    string
		process processinfo.Process
		want    bool
	}{
		{name: "native worker", process: worker, want: true},
		{name: "unrelated executable with hint", process: processinfo.Process{
			PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/other",
			Args: []string{"other", "worker"}, AgentHint: "prime-agent", CWD: "/work",
		}},
		{name: "native CLI but not worker", process: processinfo.Process{
			PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/prime-agent",
			Args: []string{"prime-agent", "sessions", "--all", "--json"}, AgentHint: "prime-agent", CWD: "/work",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verifyPrimeCatalogListedWorker(t, at, test.process, test.want)
		})
	}
}

func verifyPrimeCatalogListedWorker(t *testing.T, at time.Time, process processinfo.Process, want bool) {
	t.Helper()
	entry := CatalogEntry{
		Harness: "prime-agent", SessionID: "durable", SessionPath: "/sessions/durable.jsonl",
		ProcessPID: 42, Current: true, Activity: registry.ActivityRunning,
		Reporter: registry.Reporter{Integration: "prime-agent-catalog", Version: 1, MultiSession: true},
	}
	processes := map[int]processinfo.Process{42: process}
	if _, ok := catalogListedProcess(entry, processes); ok != want {
		t.Fatalf("catalogListedProcess() = %t, want %t", ok, want)
	}
	if _, ok := catalogActivityReport(entry, processes); ok != want {
		t.Fatalf("catalogActivityReport() = %t, want %t", ok, want)
	}
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	watcher := New(Options{
		Store: store, Now: func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return []processinfo.Process{process}, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return []CatalogEntry{entry}, nil },
	})
	result, err := watcher.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if (result.Present == 1) != want {
		t.Fatalf("observer present = %d, want worker = %t", result.Present, want)
	}
	assertPrimeCatalogWorkerSession(t, store, want)
}

func assertPrimeCatalogWorkerSession(t *testing.T, store registry.Store, want bool) {
	t.Helper()
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if !want {
		if len(sessions) != 0 {
			t.Fatalf("unrelated process claimed native session = %#v", sessions)
		}
		return
	}
	if len(sessions) != 1 || sessions[0].SessionID != "durable" || sessions[0].Process == nil ||
		sessions[0].Process.PID != 42 || sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("valid catalog worker = %#v", sessions)
	}
}

func TestPrimeCatalogInvalidPIDKeepsSavedMetadata(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	if _, err := store.Observe(t.Context(), registry.Observation{
		Harness: "prime-agent", At: at.Add(-time.Minute),
		Subject:  registry.ObservationIdentity{SessionID: "durable"},
		Evidence: &registry.Report{Event: "session.started"},
	}); err != nil {
		t.Fatal(err)
	}
	process := processinfo.Process{
		PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/other",
		Args: []string{"other", "worker"}, AgentHint: "prime-agent", CWD: "/work",
	}
	watcher := New(Options{
		Store: store, Now: func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return []processinfo.Process{process}, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) {
			return []CatalogEntry{{
				Harness: "prime-agent", SessionID: "durable", SessionPath: "/sessions/durable.jsonl",
				ResumeCommand: []string{"prime-agent", "--resume", "/sessions/durable.jsonl"},
				CWD:           "/work", ProjectRoot: "/work", ProcessPID: 42, Current: true,
				Activity: registry.ActivityRunning,
				Reporter: registry.Reporter{Integration: "prime-agent-catalog", Version: 1, MultiSession: true},
			}}, nil
		},
	})
	if result, err := watcher.RunOnce(t.Context()); err != nil || result.Present != 0 {
		t.Fatalf("observer cycle = %#v, error %v", result, err)
	}
	assertSavedPrimeCatalogMetadata(t, store)
}

func assertSavedPrimeCatalogMetadata(t *testing.T, store registry.Store) {
	t.Helper()
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("saved sessions = %#v, error %v", sessions, err)
	}
	session := sessions[0]
	if session.Process != nil || session.Observations.Catalog == nil || session.Observations.Catalog.ProcessPID != 0 ||
		session.SessionPath != "/sessions/durable.jsonl" || session.CWD != "/work" || session.Observations.Native == nil ||
		session.Observations.Native.Reporter.Integration == "prime-agent-catalog" {
		t.Fatalf("invalid PID attached to saved session or metadata lost: %#v", session)
	}
}

func assertCatalogActivityReport(t *testing.T, report *registry.Report, ok bool) {
	t.Helper()
	if !ok || report.Process == nil || !report.Process.Complete() || report.Process.Executable != "/bin/goose" ||
		report.Process.PPID != 1 || report.Process.ProcessGroupID != 42 || report.Process.CWD != "/work" {
		t.Fatalf("catalogActivityReport() = %#v, %v", report, ok)
	}
	if report.Activity == nil || *report.Activity != registry.ActivityRunning || report.Attributes["native_title"] != "example" {
		t.Fatalf("catalog activity report = %#v", report)
	}
}

//nolint:cyclop // regression verifies activity, association, and metadata across consecutive catalog snapshots
func TestObserverAppliesNativeCatalogActivityEachCycle(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	activity := registry.ActivityIdle
	process := processinfo.Process{PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/goose", CWD: "/work"}
	watcher := New(Options{
		Store: store, Now: func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return []processinfo.Process{process}, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) {
			return []CatalogEntry{{
				Harness: "goose", SessionID: "native", ProcessPID: 42, Current: true, Activity: activity,
				Attributes: map[string]string{"native_title": "example"},
				Reporter:   registry.Reporter{Integration: "goose-hook", Version: 3, MultiSession: true},
			}}, nil
		},
	})
	if _, err := watcher.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("first cycle sessions = %#v, err = %v", sessions, err)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle {
		t.Fatalf("first catalog activity = %#v", sessions[0])
	}
	if sessions[0].Observations.Native == nil || sessions[0].Observations.Native.Attributes["native_title"] != "example" {
		t.Fatalf("first catalog metadata = %#v", sessions[0])
	}
	activity = registry.ActivityRunning
	// A fast repeated snapshot may have the same observer timestamp. The native
	// report must still be accepted rather than conflict with the prior state.
	if _, err := watcher.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessions, err = store.List(t.Context(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("second cycle sessions = %#v, err = %v", sessions, err)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("second catalog state = %#v", sessions[0])
	}
}
