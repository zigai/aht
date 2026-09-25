package observer

import (
	"testing"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
)

func paneProcess(pane tmux.Pane, processes []processinfo.Process, byPID map[int]processinfo.Process, harnessByPID map[int]registry.Harness) (processinfo.Process, registry.Harness, bool) {
	converted := multiplexerPanesFromTmux([]tmux.Pane{pane})
	return multiplexerPaneProcess(converted[0], processes, byPID, harnessByPID, commandPaneCounts(converted))
}

func TestPaneProcessPrefersDirectAgentOverForegroundWrapper(t *testing.T) {
	t.Parallel()
	wrapper := processinfo.Process{PID: 30, PPID: 10, ProcessGroupID: 30, Foreground: true, StartIdentity: "boot:30", Executable: "fence", TTY: "/dev/pts/5", AgentHint: "pi", Args: []string{"fence", "--", "pi"}}
	direct := processinfo.Process{PID: 31, PPID: 30, ProcessGroupID: 30, Foreground: true, StartIdentity: "boot:31", Executable: "pi", TTY: "/dev/pts/5", Args: []string{"pi"}}
	processes := []processinfo.Process{wrapper, direct}
	byPID := map[int]processinfo.Process{30: wrapper, 31: direct}
	harnessByPID := map[int]registry.Harness{30: registry.Harness("pi"), 31: registry.Harness("pi")}
	pane := tmux.Pane{Tmux: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%5", PaneTTY: "/dev/pts/5", PanePID: 10}, ServerIdentity: "default", PanePID: 10, PaneTTY: "/dev/pts/5"}
	got, harnessID, ok := paneProcess(pane, processes, byPID, harnessByPID)
	if !ok || got.PID != direct.PID || harnessID != registry.Harness("pi") {
		t.Fatalf("paneProcess = process %#v harness %q ok %v, want direct Pi", got, harnessID, ok)
	}
}

func TestPaneProcessPrefersForegroundAgentOnControllingTTY(t *testing.T) {
	t.Parallel()
	background := processinfo.Process{PID: 20, PPID: 10, ProcessGroupID: 20, Foreground: false, StartIdentity: "boot:20", Executable: "codex", TTY: "/dev/pts/4", Args: []string{"codex"}}
	foreground := processinfo.Process{PID: 21, PPID: 10, ProcessGroupID: 21, Foreground: true, StartIdentity: "boot:21", Executable: "claude", TTY: "/dev/pts/4", Args: []string{"claude"}}
	processes := []processinfo.Process{background, foreground}
	byPID := map[int]processinfo.Process{20: background, 21: foreground}
	harnessByPID := map[int]registry.Harness{20: registry.Harness("codex"), 21: registry.Harness("claude")}
	pane := tmux.Pane{Tmux: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%4", PaneTTY: "/dev/pts/4", PanePID: 10}, ServerIdentity: "default", PanePID: 10, PaneTTY: "/dev/pts/4"}
	got, harness, ok := paneProcess(pane, processes, byPID, harnessByPID)
	if !ok || got.PID != foreground.PID || harness != registry.Harness("claude") {
		t.Fatalf("paneProcess = process %#v harness %q ok %v, want foreground Claude", got, harness, ok)
	}
}
