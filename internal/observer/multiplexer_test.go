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

func TestObserverCorrelatesZellijEnvironmentIdentityAndDetectsScreen(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 1, ProcessGroupID: 42, Foreground: true, StartIdentity: "boot:42",
		Executable: "/usr/bin/codex", CWD: "/repo", MultiplexerKind: "zellij",
		MultiplexerSession: "work", MultiplexerPane: "7",
	}
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerZellij, SessionName: "work", TabID: "3", TabName: "agents",
			PaneID: "terminal_7", PaneCurrentPath: "/repo",
		},
		Command: "codex", CWD: "/repo", Title: "Codex",
	}
	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{pane}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› next task\nContext 63% used", Title: "Codex"}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerZellij || sessions[0].Location.PaneID != "terminal_7" {
		t.Fatalf("zellij session = %#v", sessions)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle || sessions[0].Decision() == nil || sessions[0].Decision().Authority != "screen" {
		t.Fatalf("zellij activity = %#v", sessions[0])
	}
}

func TestObserverUsesHerdrForegroundProcessAndSemanticState(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42",
		Executable: "/usr/bin/claude", CWD: "/repo",
	}
	waiting := registry.ActivityWaiting
	pane := mux.Pane{
		Location: registry.Location{
			Kind: registry.MultiplexerHerdr, SessionName: "work", WorkspaceID: "w1", TabID: "w1:t1",
			PaneID: "w1:p1", PaneCurrentPath: "/repo", PanePID: 42,
		},
		Processes: []mux.ProcessRef{{PID: 42, ProcessGroupID: 42, Command: "claude", CWD: "/repo"}},
		Activity:  &waiting, StateReason: "herdr_agent_status",
	}
	captureCalled := false
	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) {
			return []mux.Pane{pane}, nil
		},
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			captureCalled = true
			return mux.ScreenSnapshot{}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if captureCalled {
		t.Fatal("observer captured the Herdr buffer despite semantic agent state")
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr || sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("herdr session = %#v", sessions)
	}
	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityWaiting || sessions[0].Decision() == nil || sessions[0].Decision().Authority != registry.AuthorityScreen || sessions[0].Decision().Reason != "herdr_agent_status" {
		t.Fatalf("herdr activity = %#v", sessions[0])
	}
}

func TestObserverPrefersRicherNestedMultiplexerLocation(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 40, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/codex", CWD: "/repo",
		MultiplexerKind: "zellij", MultiplexerSession: "outer", MultiplexerPane: "7",
	}
	panes := []mux.Pane{
		{
			Location: registry.Location{Kind: registry.MultiplexerZellij, SessionName: "outer", PaneID: "terminal_7", PaneCurrentPath: "/repo"},
			Command:  "codex", CWD: "/repo",
		},
		{
			Location:  registry.Location{Kind: registry.MultiplexerHerdr, SessionName: "inner", WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1", PaneCurrentPath: "/repo"},
			Processes: []mux.ProcessRef{{PID: 42, ProcessGroupID: 42, Command: "codex", CWD: "/repo"}},
		},
	}
	observer := New(Options{
		Store: store,
		ProcessList: func(context.Context) ([]processinfo.Process, error) {
			return []processinfo.Process{process}, nil
		},
		PaneList: func(context.Context) ([]mux.Pane, error) { return panes, nil },
		ScreenCapture: func(context.Context, mux.Pane) (mux.ScreenSnapshot, error) {
			return mux.ScreenSnapshot{Text: "› next task"}, nil
		},
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if _, err := observer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr || sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("nested location = %#v", sessions)
	}
}

func TestCommandCWDFallbackRequiresUniquePane(t *testing.T) {
	t.Parallel()
	process := processinfo.Process{PID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/codex", CWD: "/repo"}
	panes := []mux.Pane{
		{Location: registry.Location{Kind: registry.MultiplexerZellij, SessionName: "one", PaneID: "terminal_1"}, Command: "codex", CWD: "/repo"},
		{Location: registry.Location{Kind: registry.MultiplexerZellij, SessionName: "two", PaneID: "terminal_2"}, Command: "codex", CWD: "/repo"},
	}
	counts := commandPaneCounts(panes)
	byPID := map[int]processinfo.Process{42: process}
	harnessByPID := map[int]registry.Harness{42: registry.Harness("codex")}
	if _, _, ok := multiplexerPaneProcess(panes[0], []processinfo.Process{process}, byPID, harnessByPID, counts); ok {
		t.Fatal("ambiguous command/cwd fallback correlated a pane")
	}
}
