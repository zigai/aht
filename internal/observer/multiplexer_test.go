package observer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

func multiplexerPanesFromTmux(panes []tmux.Pane) []mux.Pane {
	result := make([]mux.Pane, 0, len(panes))
	for _, pane := range panes {
		result = append(result, pane.ToMuxPane())
	}

	return result
}

func TestObserverCorrelatesZellijEnvironmentIdentityAndDetectsScreen(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID: 42, PPID: 1, ProcessGroupID: 42, Foreground: true, StartIdentity: "boot:42",
		Executable: "/usr/bin/codex", CWD: "/repo",
		Env: map[string]string{"ZELLIJ_SESSION_NAME": "work", "ZELLIJ_PANE_ID": "7"},
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

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerZellij ||
		sessions[0].Location.PaneID != "terminal_7" {
		t.Fatalf("zellij session = %#v", sessions)
	}

	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityIdle ||
		sessions[0].Decision() == nil ||
		sessions[0].Decision().Authority != "screen" {
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

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr ||
		sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("herdr session = %#v", sessions)
	}

	if sessions[0].Activity() == nil || *sessions[0].Activity() != registry.ActivityWaiting ||
		sessions[0].Decision() == nil ||
		sessions[0].Decision().Authority != registry.AuthorityScreen ||
		sessions[0].Decision().Reason != "herdr_agent_status" {
		t.Fatalf("herdr activity = %#v", sessions[0])
	}
}

func TestObserverPrefersRicherNestedMultiplexerLocation(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), catalog.Rules{})
	process := processinfo.Process{
		PID:            42,
		PPID:           40,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/codex",
		CWD:            "/repo",
		Env:            map[string]string{"ZELLIJ_SESSION_NAME": "outer", "ZELLIJ_PANE_ID": "7"},
	}
	panes := []mux.Pane{
		{
			Location: registry.Location{
				Kind:            registry.MultiplexerZellij,
				SessionName:     "outer",
				PaneID:          "terminal_7",
				PaneCurrentPath: "/repo",
			},
			Command: "codex",
			CWD:     "/repo",
		},
		{
			Location: registry.Location{
				Kind:            registry.MultiplexerHerdr,
				SessionName:     "inner",
				WorkspaceID:     "w1",
				TabID:           "w1:t1",
				PaneID:          "w1:p1",
				PaneCurrentPath: "/repo",
			},
			Processes: []mux.ProcessRef{
				{PID: 42, ProcessGroupID: 42, Command: "codex", CWD: "/repo"},
			},
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

	if len(sessions) != 1 || sessions[0].Location.Kind != registry.MultiplexerHerdr ||
		sessions[0].Location.PaneID != "w1:p1" {
		t.Fatalf("nested location = %#v", sessions)
	}
}

var errStubCapture = errors.New("stub capture failed")

type stubDriver struct {
	mux.Driver

	kind registry.MultiplexerKind
}

func (d stubDriver) CapturePane(_ context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	if pane.Title == "closed" {
		return mux.ScreenSnapshot{}, errStubCapture
	}

	return mux.ScreenSnapshot{Text: string(d.kind) + " " + pane.Location.PaneID, Title: ""}, nil
}

type batchStubDriver struct {
	stubDriver
}

func (d batchStubDriver) CapturePanes(ctx context.Context, panes []mux.Pane) []mux.ScreenCapture {
	captures := captureEachPane(d.CapturePane)(ctx, panes)
	for i := range captures {
		captures[i].Snapshot.Title = fmt.Sprintf("batch of %d", len(panes))
	}

	return captures
}

func TestCaptureDriverPanesKeepsEachPaneWithItsDriver(t *testing.T) {
	t.Parallel()

	pane := func(kind registry.MultiplexerKind, id string, title string) mux.Pane {
		return mux.Pane{
			Location: registry.Location{Kind: kind, PaneID: id},
			Command:  "",
			CWD:      "",
			Title:    title,
		}
	}
	panes := []mux.Pane{
		pane("batching", "a1", ""),
		pane("single", "b1", ""),
		pane("unknown", "x1", ""),
		pane("batching", "a2", "closed"),
		pane("single", "b2", "closed"),
		pane("batching", "a3", ""),
		pane("single", "b3", ""),
	}

	drivers := map[registry.MultiplexerKind]mux.Driver{
		"batching": batchStubDriver{stubDriver{Driver: nil, kind: "batching"}},
		"single":   stubDriver{Driver: nil, kind: "single"},
	}
	find := func(kind registry.MultiplexerKind) (mux.Driver, bool) {
		driver, ok := drivers[kind]

		return driver, ok
	}

	captures := captureDriverPanes(context.Background(), panes, find)

	want := []struct {
		text, title string
		err         error
	}{
		{text: "batching a1", title: "batch of 3", err: nil},
		{text: "single b1", title: "", err: nil},
		{text: "", title: "", err: errUnsupportedMultiplexerPane},
		{text: "", title: "", err: errStubCapture},
		{text: "", title: "", err: errStubCapture},
		{text: "batching a3", title: "batch of 3", err: nil},
		{text: "single b3", title: "", err: nil},
	}
	if len(captures) != len(want) {
		t.Fatalf("captures = %d, want %d", len(captures), len(want))
	}

	for i, capture := range captures {
		if capture.Snapshot.Text != want[i].text || capture.Snapshot.Title != want[i].title ||
			!errors.Is(capture.Err, want[i].err) {
			t.Errorf("pane %s capture = %#v, want %#v", panes[i].Location.PaneID, capture, want[i])
		}
	}
}

func TestCommandCWDFallbackRequiresUniquePane(t *testing.T) {
	t.Parallel()

	process := processinfo.Process{
		PID:           42,
		StartIdentity: "boot:42",
		Executable:    "/usr/bin/codex",
		CWD:           "/repo",
	}
	panes := []mux.Pane{
		{
			Location: registry.Location{
				Kind:        registry.MultiplexerZellij,
				SessionName: "one",
				PaneID:      "terminal_1",
			},
			Command: "codex",
			CWD:     "/repo",
		},
		{
			Location: registry.Location{
				Kind:        registry.MultiplexerZellij,
				SessionName: "two",
				PaneID:      "terminal_2",
			},
			Command: "codex",
			CWD:     "/repo",
		},
	}
	counts := commandPaneCounts(panes)
	byPID := map[int]processinfo.Process{42: process}

	harnessByPID := map[int]registry.Harness{42: registry.Harness("codex")}
	if _, _, ok := multiplexerPaneProcess(
		panes[0],
		[]processinfo.Process{process},
		newProcessTree(byPID),
		harnessByPID,
		counts,
	); ok {
		t.Fatal("ambiguous command/cwd fallback correlated a pane")
	}
}
