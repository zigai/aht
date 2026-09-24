package observer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

func TestResolveHarnessIgnoresLaterArguments(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{Executable: "/usr/bin/tmux", Args: []string{"/usr/bin/tmux", "new-session", "-s", "agent-test", "/tmp/codex"}}
	if harnessID, ok := resolveHarness(process); ok || harnessID != "" {
		t.Fatalf("tmux launcher was classified as harness: %q", harnessID)
	}
	process.Args = []string{"/tmp/codex", "resume"}
	if harnessID, ok := resolveHarness(process); !ok || harnessID != registry.Harness("codex") {
		t.Fatalf("codex argv was not classified: %q %t", harnessID, ok)
	}
}

func TestObserverCatalogCorrelatesCurrentClaudeProcess(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)
	process := processinfo.Process{PID: 42, PPID: 1, ProcessGroupID: 42, StartIdentity: "boot:A", Executable: "/usr/bin/claude"}
	watcher := New(Options{StorePath: path, Now: func() time.Time { return at }, ProcessList: func(context.Context) ([]processinfo.Process, error) { return []processinfo.Process{process}, nil }, PaneList: func(context.Context) ([]mux.Pane, error) { return nil, nil }, CatalogList: func(context.Context) ([]CatalogEntry, error) {
		return []CatalogEntry{{Harness: registry.Harness("claude"), SessionID: "agent-1", ProcessPID: 42, Current: true}}, nil
	}})
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := registry.NewJournal(path, catalog.Rules{}).List(context.Background(), registry.Filter{})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("correlated sessions: %v %#v", err, sessions)
	}
	session := sessions[0]
	if session.Presence() != registry.PresenceLive || session.SessionID != "agent-1" {
		t.Fatalf("correlated session: %#v", session)
	}
	if session.Observations.Catalog == nil || session.Observations.Process == nil {
		t.Fatalf("missing source evidence: %#v", session.Observations)
	}
}

func TestObserverCorrelatesAgentAcrossIntermediateShells(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	processes := []processinfo.Process{
		{PID: 10, PPID: 1, StartIdentity: "boot:10", Executable: "/bin/bash", CWD: "/pane"},
		{PID: 11, PPID: 10, StartIdentity: "boot:11", Executable: "/bin/sh", CWD: "/pane"},
		{PID: 12, PPID: 11, StartIdentity: "boot:12", Executable: "/usr/bin/codex", CWD: "/agent"},
	}
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerTmux, ServerID: "default", SessionName: "work",
			PaneID: "%7", PanePID: 10, PaneCurrentPath: "/pane",
		},
		Processes: []mux.ProcessRef{{PID: 10}},
	}
	watcher := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processes, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{pane}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› ready"}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Process == nil || sessions[0].Process.PID != 12 || sessions[0].Location.PaneID != "%7" {
		t.Fatalf("intermediate-shell correlation = %#v", sessions)
	}
}

func TestObserverSuppressesWrapperWhenDirectAgentChildExists(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	processes := []processinfo.Process{
		{
			PID: 20, PPID: 1, StartIdentity: "boot:20", Executable: "/usr/bin/env",
			Args: []string{"/usr/bin/env", "AGENT_MODE=1", "codex"},
		},
		{
			PID: 21, PPID: 20, StartIdentity: "boot:21", Executable: "/usr/bin/codex",
			Args: []string{"/usr/bin/codex"},
		},
	}
	watcher := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return processes, nil
		},
		PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
	})
	result, err := watcher.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Present != 1 || len(sessions) != 1 || sessions[0].Process == nil || sessions[0].Process.PID != 21 {
		t.Fatalf("wrapper/direct-child observations = result %#v sessions %#v", result, sessions)
	}
}
