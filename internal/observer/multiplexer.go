package observer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	harness "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/herdr"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
	"github.com/zigai/aht/pkg/tmux"
	"github.com/zigai/aht/pkg/zellij"
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

func listMultiplexerPanes(ctx context.Context) ([]mux.Pane, error) {
	panes := make([]mux.Pane, 0)
	var listErrors []error
	for _, driver := range defaultMultiplexerDrivers {
		listed, listErr := driver.ListPanes(ctx)
		if listErr != nil {
			listErrors = append(listErrors, listErr)
			continue
		}
		panes = append(panes, listed...)
	}
	return panes, errors.Join(listErrors...)
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
				return mux.ScreenSnapshot{}, fmt.Errorf("capture %s pane: %w", pane.Location.Kind, err)
			}
			return snapshot, nil
		}
	}
	return mux.ScreenSnapshot{}, errUnsupportedMultiplexerPane
}

func multiplexerPaneProcess(pane mux.Pane, processes []processinfo.Process, byPID map[int]processinfo.Process, harnessByPID map[int]registry.Harness, paneCommandCounts map[string]int) (processinfo.Process, registry.Harness, bool) {
	if process, harnessID, ok := foregroundPaneProcess(pane, processes, harnessByPID); ok {
		return process, harnessID, true
	}
	for _, reference := range pane.Processes {
		if process, ok := byPID[reference.PID]; ok {
			if harnessID, ok := harnessByPID[process.PID]; ok {
				return process, harnessID, true
			}
		}
		if process, harnessID, ok := descendantHarnessProcess(reference.PID, processes, byPID, harnessByPID); ok {
			return process, harnessID, true
		}
	}
	if process, harnessID, ok := processMatchingMultiplexerIdentity(pane, processes, harnessByPID); ok {
		return process, harnessID, true
	}
	if process, harnessID, ok := commandPaneProcess(pane, processes, harnessByPID, paneCommandCounts); ok {
		return process, harnessID, true
	}
	var empty processinfo.Process
	return empty, "", false
}

func processMatchingMultiplexerIdentity(pane mux.Pane, processes []processinfo.Process, harnessByPID map[int]registry.Harness) (processinfo.Process, registry.Harness, bool) {
	var selected processinfo.Process
	var selectedHarness registry.Harness
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

//nolint:cyclop // each multiplexer has different native identity guarantees
func multiplexerIdentityMatches(process processinfo.Process, location registry.MultiplexerContext) bool {
	if process.MultiplexerKind == "" || process.MultiplexerKind != string(location.Kind) {
		return false
	}
	switch location.Kind {
	case registry.MultiplexerZellij:
		if process.MultiplexerSession == "" || location.SessionName == "" || process.MultiplexerSession != location.SessionName {
			return false
		}
	case registry.MultiplexerHerdr:
		sameServer := process.MultiplexerServer != "" && location.ServerID != "" && process.MultiplexerServer == location.ServerID
		sameSession := process.MultiplexerSession != "" && location.SessionName != "" && process.MultiplexerSession == location.SessionName
		if !sameServer && !sameSession {
			return false
		}
	case registry.MultiplexerTmux:
		return false
	}
	return mux.NormalizePaneID(location.Kind, process.MultiplexerPane) == mux.NormalizePaneID(location.Kind, location.PaneID)
}

func foregroundPaneProcess(pane mux.Pane, processes []processinfo.Process, harnessByPID map[int]registry.Harness) (processinfo.Process, registry.Harness, bool) {
	var selected processinfo.Process
	var selectedHarness registry.Harness
	for _, process := range processes {
		if !process.Foreground || pane.ProcessTTY == "" || process.TTY != pane.ProcessTTY {
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

func descendantHarnessProcess(rootPID int, processes []processinfo.Process, byPID map[int]processinfo.Process, harnessByPID map[int]registry.Harness) (processinfo.Process, registry.Harness, bool) {
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

func commandPaneProcess(pane mux.Pane, processes []processinfo.Process, harnessByPID map[int]registry.Harness, paneCommandCounts map[string]int) (processinfo.Process, registry.Harness, bool) {
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
	paneHarness, ok := harness.FromCommand(filepath.Base(fields[0]))
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
