package observer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/herdr"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
	"github.com/zigai/aht/v2/pkg/tmux"
	"github.com/zigai/aht/v2/pkg/zellij"
)

const (
	multiplexerPriorityTmux   = 1
	multiplexerPriorityZellij = 2
	multiplexerPriorityHerdr  = 3
)

var (
	errUnsupportedMultiplexerPane = errors.New("unsupported multiplexer pane")

	defaultMultiplexerDrivers = []mux.Driver{
		tmux.NewDriver(),
		zellij.NewDriver(),
		herdr.NewDriver(),
	}
)

func listMultiplexerPanes(
	ctx context.Context,
	processes []processinfo.Process,
) ([]mux.Pane, error) {
	panes := make([]mux.Pane, 0)

	var listErrors []error

	for _, driver := range defaultMultiplexerDrivers {
		// Reuse the cycle's process scan instead of rescanning /proc for tmux servers.
		if tmuxDriver, ok := driver.(tmux.Driver); ok {
			tmuxDriver.ServerProcesses = func(context.Context) ([]tmux.ServerProcess, error) {
				return tmuxServerProcesses(processes), nil
			}
			driver = tmuxDriver
		}

		listed, listErr := driver.ListPanes(ctx)
		if listErr != nil {
			listErrors = append(listErrors, listErr)
			continue
		}

		panes = append(panes, listed...)
	}

	return panes, errors.Join(listErrors...)
}

func tmuxServerProcesses(processes []processinfo.Process) []tmux.ServerProcess {
	servers := make([]tmux.ServerProcess, 0)

	for _, process := range processes {
		if server, ok := tmux.ServerProcessFrom(process.PID, process.Name, process.Args); ok {
			servers = append(servers, server)
		}
	}

	return servers
}

func locationPresence(ctx context.Context, location registry.Location) (registry.Presence, error) {
	if location.Kind == registry.MultiplexerTmux {
		presence, err := tmux.PanePresence(ctx, location)
		if err != nil {
			return registry.PresenceUnknown, fmt.Errorf("check tmux pane: %w", err)
		}

		return presence, nil
	}

	return registry.PresenceUnknown, nil
}

func multiplexerPanesFromTmux(panes []tmux.Pane) []mux.Pane {
	result := make([]mux.Pane, 0, len(panes))
	for _, pane := range panes {
		result = append(result, pane.ToMuxPane())
	}

	return result
}

func captureMultiplexerPane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	for _, driver := range defaultMultiplexerDrivers {
		if driver.Kind() == pane.Location.Kind {
			snapshot, err := driver.CapturePane(ctx, pane)
			if err != nil {
				return mux.ScreenSnapshot{}, fmt.Errorf(
					"capture %s pane: %w",
					pane.Location.Kind,
					err,
				)
			}

			return snapshot, nil
		}
	}

	return mux.ScreenSnapshot{}, errUnsupportedMultiplexerPane
}

func multiplexerPaneProcess(
	pane mux.Pane,
	processes []processinfo.Process,
	byPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
	paneCommandCounts map[string]int,
) (processinfo.Process, registry.Harness, bool) {
	if process, harnessID, ok := foregroundPaneProcess(pane, processes, harnessByPID); ok {
		return process, harnessID, true
	}

	for _, reference := range pane.Processes {
		if process, ok := byPID[reference.PID]; ok {
			if harnessID, ok := harnessByPID[process.PID]; ok {
				return process, harnessID, true
			}
		}

		if process, harnessID, ok := descendantHarnessProcess(
			reference.PID,
			processes,
			byPID,
			harnessByPID,
		); ok {
			return process, harnessID, true
		}
	}

	if process, harnessID, ok := processMatchingMultiplexerIdentity(
		pane,
		processes,
		harnessByPID,
	); ok {
		return process, harnessID, true
	}

	if process, harnessID, ok := commandPaneProcess(
		pane,
		processes,
		harnessByPID,
		paneCommandCounts,
	); ok {
		return process, harnessID, true
	}

	var empty processinfo.Process

	return empty, "", false
}

func processTiedToPane(
	process processinfo.Process,
	pane mux.Pane,
	byPID map[int]processinfo.Process,
) bool {
	location := paneLocation(pane)
	if location.PaneTTY != "" && process.TTY == location.PaneTTY {
		return true
	}

	if process.PID == location.PanePID || processDescendsFrom(process, location.PanePID, byPID) {
		return true
	}

	for _, reference := range pane.Processes {
		if process.PID == reference.PID || processDescendsFrom(process, reference.PID, byPID) {
			return true
		}
	}

	return multiplexerIdentityMatches(process, location)
}

func processDescendsFrom(
	process processinfo.Process,
	rootPID int,
	byPID map[int]processinfo.Process,
) bool {
	if rootPID <= 0 {
		return false
	}

	for range len(byPID) {
		if process.PPID == rootPID {
			return true
		}

		parent, ok := byPID[process.PPID]
		if !ok || parent.PID == process.PID {
			break
		}

		process = parent
	}

	return false
}

func normalizeHarnessForeground(
	processes []processinfo.Process,
	byPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
) {
	for index, process := range processes {
		if !process.Foreground || harnessByPID[process.PID] == "" {
			continue
		}

		if nestedHarnessProcess(process, byPID, harnessByPID) {
			// The kernel foreground flag covers a process group, including nested noninteractive agents.
			processes[index].Foreground = false
		}
	}

	for _, process := range processes {
		byPID[process.PID] = process
	}
}

func nestedHarnessProcess(
	process processinfo.Process,
	byPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
) bool {
	ancestor := process
	for range len(byPID) {
		parent, ok := byPID[ancestor.PPID]
		if !ok || parent.PID == ancestor.PID || parent.PID == process.PID {
			break
		}

		if harnessByPID[parent.PID] != "" {
			return true
		}

		ancestor = parent
	}

	return false
}

func paneForegroundKnown(pane mux.Pane, processes []processinfo.Process) bool {
	tty := paneLocation(pane).PaneTTY
	for _, process := range processes {
		if tty != "" && process.TTY == tty && (process.Foreground || process.ProcessGroupID > 0) {
			return true
		}
	}

	return false
}

func processMatchingMultiplexerIdentity(
	pane mux.Pane,
	processes []processinfo.Process,
	harnessByPID map[int]registry.Harness,
) (processinfo.Process, registry.Harness, bool) {
	var (
		selected        processinfo.Process
		selectedHarness registry.Harness
	)

	for _, process := range processes {
		harnessID, ok := harnessByPID[process.PID]
		if !ok || !multiplexerIdentityMatches(process, pane.Location) {
			continue
		}

		if selected.PID == 0 || preferForegroundProcess(process, selected) {
			selected, selectedHarness = process, harnessID
		}
	}

	return selected, selectedHarness, selected.PID != 0
}

func multiplexerIdentityMatches(process processinfo.Process, location registry.Location) bool {
	if process.MultiplexerKind == "" || process.MultiplexerKind != string(location.Kind) {
		return false
	}

	switch location.Kind {
	case registry.MultiplexerZellij:
		if process.MultiplexerSession == "" || location.SessionName == "" ||
			process.MultiplexerSession != location.SessionName {
			return false
		}
	case registry.MultiplexerHerdr:
		if !herdrIdentityMatches(process, location) {
			return false
		}
	case registry.MultiplexerTmux:
		return false
	}

	return mux.NormalizePaneID(
		location.Kind,
		process.MultiplexerPane,
	) == mux.NormalizePaneID(
		location.Kind,
		location.PaneID,
	)
}

func herdrIdentityMatches(process processinfo.Process, location registry.Location) bool {
	sameServer := process.MultiplexerServer != "" && location.ServerID != "" &&
		process.MultiplexerServer == location.ServerID
	sameSession := process.MultiplexerSession != "" && location.SessionName != "" &&
		process.MultiplexerSession == location.SessionName

	return sameServer || sameSession
}

func foregroundPaneProcess(
	pane mux.Pane,
	processes []processinfo.Process,
	harnessByPID map[int]registry.Harness,
) (processinfo.Process, registry.Harness, bool) {
	var (
		selected        processinfo.Process
		selectedHarness registry.Harness
	)

	tty := paneLocation(pane).PaneTTY
	for _, process := range processes {
		if !process.Foreground || tty == "" || process.TTY != tty {
			continue
		}

		harnessID, ok := harnessByPID[process.PID]
		if !ok {
			continue
		}

		if selected.PID == 0 || preferForegroundProcess(process, selected) {
			selected, selectedHarness = process, harnessID
		}
	}

	return selected, selectedHarness, selected.PID != 0
}

func descendantHarnessProcess(
	rootPID int,
	processes []processinfo.Process,
	byPID map[int]processinfo.Process,
	harnessByPID map[int]registry.Harness,
) (processinfo.Process, registry.Harness, bool) {
	if rootPID <= 0 {
		var empty processinfo.Process
		return empty, "", false
	}

	for _, process := range processes {
		ancestor := process
		for range processes {
			if ancestor.PPID == rootPID {
				if harnessID, ok := harnessByPID[process.PID]; ok {
					return process, harnessID, true
				}
			}

			next, ok := byPID[ancestor.PPID]
			if !ok || next.PID == ancestor.PID {
				break
			}

			ancestor = next
		}
	}

	var empty processinfo.Process

	return empty, "", false
}

func commandPaneProcess(
	pane mux.Pane,
	processes []processinfo.Process,
	harnessByPID map[int]registry.Harness,
	paneCommandCounts map[string]int,
) (processinfo.Process, registry.Harness, bool) {
	key, paneHarness, ok := commandPaneKey(pane)
	if !ok || paneCommandCounts[key] != 1 {
		var empty processinfo.Process
		return empty, "", false
	}

	var selected processinfo.Process

	for _, process := range processes {
		if harnessByPID[process.PID] != paneHarness {
			continue
		}

		if process.CWD == "" || pane.CWD != process.CWD {
			continue
		}

		if selected.PID != 0 {
			var empty processinfo.Process
			return empty, "", false
		}

		selected = process
	}

	return selected, paneHarness, selected.PID != 0
}

func commandPaneCounts(panes []mux.Pane) map[string]int {
	counts := make(map[string]int)

	for _, pane := range panes {
		if key, _, ok := commandPaneKey(pane); ok {
			counts[key]++
		}
	}

	return counts
}

func commandPaneKey(pane mux.Pane) (string, registry.Harness, bool) {
	if pane.CWD == "" {
		return "", "", false
	}

	fields := strings.Fields(pane.Command)
	if len(fields) == 0 {
		return "", "", false
	}

	paneHarness, ok := catalog.FromCommand(filepath.Base(fields[0]))
	if !ok {
		return "", "", false
	}

	return string(paneHarness) + "\x00" + filepath.Clean(pane.CWD), paneHarness, true
}

func multiplexerPriority(kind registry.MultiplexerKind) int {
	switch kind {
	case registry.MultiplexerHerdr:
		return multiplexerPriorityHerdr
	case registry.MultiplexerZellij:
		return multiplexerPriorityZellij
	case registry.MultiplexerTmux:
		return multiplexerPriorityTmux
	default:
		return 0
	}
}
