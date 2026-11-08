package observer

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

func paneProcess(
	pane tmux.Pane,
	processes []processinfo.Process,
	byPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
) (processinfo.Process, registry.Harness, bool) {
	converted := multiplexerPanesFromTmux([]tmux.Pane{pane})

	return multiplexerPaneProcess(
		converted[0],
		processes,
		newProcessTree(byPID),
		harnessByPID,
		commandPaneCounts(converted),
	)
}

func TestPaneProcessPrefersDirectAgentOverForegroundWrapper(t *testing.T) {
	t.Parallel()

	wrapper := processinfo.Process{
		PID:            30,
		PPID:           10,
		ProcessGroupID: 30,
		Foreground:     true,
		StartIdentity:  "boot:30",
		Executable:     "fence",
		TTY:            "/dev/pts/5",
		Args:           []string{"fence", "--", "pi"},
	}
	direct := processinfo.Process{
		PID:            31,
		PPID:           30,
		ProcessGroupID: 30,
		Foreground:     true,
		StartIdentity:  "boot:31",
		Executable:     "pi",
		TTY:            "/dev/pts/5",
		Args:           []string{"pi"},
	}
	processes := []processinfo.Process{wrapper, direct}
	byPID := map[int]processinfo.Process{30: wrapper, 31: direct}
	harnessByPID := map[int]registry.Harness{30: registry.Harness("pi"), 31: registry.Harness("pi")}
	pane := tmux.Pane{
		Location: registry.Location{
			Kind:     registry.MultiplexerTmux,
			ServerID: "default",
			PaneID:   "%5",
			PaneTTY:  "/dev/pts/5",
			PanePID:  10,
		},
		PanePID: 10,
		PaneTTY: "/dev/pts/5",
	}

	got, harnessID, ok := paneProcess(pane, processes, byPID, harnessByPID)
	if !ok || got.PID != direct.PID || harnessID != registry.Harness("pi") {
		t.Fatalf("paneProcess = process %#v harness %q ok %v, want direct Pi", got, harnessID, ok)
	}
}

func TestPaneProcessPrefersForegroundAgentOnControllingTTY(t *testing.T) {
	t.Parallel()

	background := processinfo.Process{
		PID:            20,
		PPID:           10,
		ProcessGroupID: 20,
		Foreground:     false,
		StartIdentity:  "boot:20",
		Executable:     "codex",
		TTY:            "/dev/pts/4",
		Args:           []string{"codex"},
	}
	foreground := processinfo.Process{
		PID:            21,
		PPID:           10,
		ProcessGroupID: 21,
		Foreground:     true,
		StartIdentity:  "boot:21",
		Executable:     "claude",
		TTY:            "/dev/pts/4",
		Args:           []string{"claude"},
	}
	processes := []processinfo.Process{background, foreground}
	byPID := map[int]processinfo.Process{20: background, 21: foreground}
	harnessByPID := map[int]registry.Harness{
		20: registry.Harness("codex"),
		21: registry.Harness("claude"),
	}
	pane := tmux.Pane{
		Location: registry.Location{
			Kind:     registry.MultiplexerTmux,
			ServerID: "default",
			PaneID:   "%4",
			PaneTTY:  "/dev/pts/4",
			PanePID:  10,
		},
		PanePID: 10,
		PaneTTY: "/dev/pts/4",
	}

	got, harness, ok := paneProcess(pane, processes, byPID, harnessByPID)
	if !ok || got.PID != foreground.PID || harness != registry.Harness("claude") {
		t.Fatalf(
			"paneProcess = process %#v harness %q ok %v, want foreground Claude",
			got,
			harness,
			ok,
		)
	}
}

func TestSuspendedHarnessKeepsPaneLocationWhenAnotherStarts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	at := time.Now().UTC().Add(-time.Minute)

	shell := processinfo.Process{
		PID: 10, PPID: 1, ProcessGroupID: 10, StartIdentity: "boot:10",
		Executable: "zsh", TTY: "/dev/pts/4", Args: []string{"zsh"},
	}
	claude := processinfo.Process{
		PID: 21, PPID: 10, ProcessGroupID: 21, Foreground: true, StartIdentity: "boot:21",
		Executable: "claude", CWD: "/work", TTY: "/dev/pts/4", Args: []string{"claude"},
	}
	pane := mux.Pane{
		Location: registry.Location{
			Kind:     registry.MultiplexerTmux,
			ServerID: "default",
			PaneID:   "%4",
			PaneTTY:  "/dev/pts/4",
			PanePID:  shell.PID,
		},
		Processes:  []mux.ProcessRef{{PID: shell.PID, ProcessGroupID: shell.PID, Command: "zsh"}},
		ProcessTTY: "/dev/pts/4",
		Command:    "claude",
		CWD:        "/work",
	}
	processes := []processinfo.Process{shell, claude}
	watcher := New(Options{
		StorePath:   path,
		Now:         func() time.Time { return at },
		ProcessList: func(context.Context) ([]processinfo.Process, error) { return processes, nil },
		PaneList:    func(context.Context) ([]mux.Pane, error) { return []mux.Pane{pane}, nil },
		CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
		LocationPresence: func(context.Context, registry.Location) (registry.Presence, error) {
			return registry.PresenceLive, nil
		},
		DisableScreenInspection: true,
	})

	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	location := sessionLocationForPID(t, path, claude.PID)
	if location.PaneID != pane.Location.PaneID {
		t.Fatalf("foreground claude location = %#v, want pane %s", location, pane.Location.PaneID)
	}

	// Ctrl-Z stops claude; codex then starts in the foreground of the same pane.
	suspended := claude
	suspended.Foreground = false
	codex := processinfo.Process{
		PID: 22, PPID: 10, ProcessGroupID: 22, Foreground: true, StartIdentity: "boot:22",
		Executable: "codex", CWD: "/work", TTY: "/dev/pts/4", Args: []string{"codex"},
	}
	processes = []processinfo.Process{shell, suspended, codex}
	pane.Command = "codex"
	at = at.Add(time.Second)

	if _, err := watcher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	location = sessionLocationForPID(t, path, claude.PID)
	if location.PaneID != pane.Location.PaneID {
		t.Fatalf("suspended claude location = %#v, want pane %s", location, pane.Location.PaneID)
	}
}

func sessionLocationForPID(t *testing.T, path string, pid int) registry.Location {
	t.Helper()

	sessions, err := registry.NewJournal(path, catalog.Rules{}).
		List(context.Background(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	index := slices.IndexFunc(sessions, func(session registry.Session) bool {
		return session.Process != nil && session.Process.PID == pid
	})
	if index < 0 {
		t.Fatalf("no session for pid %d: %#v", pid, sessions)
	}

	return sessions[index].Location
}
