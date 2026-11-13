package observer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/mux/drivers"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errUnsupportedMultiplexerPane = errors.New("unsupported multiplexer pane")

func listMultiplexerPanes(
	ctx context.Context,
	processes []processinfo.Process,
) ([]mux.Pane, error) {
	panes := make([]mux.Pane, 0)

	var listErrors []error

	for _, driver := range drivers.All() {
		listed, listErr := driver.ListPanes(ctx, processes)

		panes = append(panes, listed...)
		if listErr != nil {
			listErrors = append(listErrors, listErr)
		}
	}

	return panes, errors.Join(listErrors...)
}

func locationPresence(ctx context.Context, location registry.Location) (registry.Presence, error) {
	driver, ok := drivers.Find(location.Kind)
	if !ok {
		return registry.PresenceUnknown, nil
	}

	presence, err := driver.Presence(ctx, location)
	if err != nil {
		return registry.PresenceUnknown, fmt.Errorf("check %s pane: %w", location.Kind, err)
	}

	return presence, nil
}

func captureMultiplexerPanes(ctx context.Context, panes []mux.Pane) []mux.ScreenCapture {
	return captureDriverPanes(ctx, panes, drivers.Find)
}

func captureDriverPanes(
	ctx context.Context,
	panes []mux.Pane,
	find func(registry.MultiplexerKind) (mux.Driver, bool),
) []mux.ScreenCapture {
	captures := make([]mux.ScreenCapture, len(panes))
	byKind := make(map[registry.MultiplexerKind][]int)

	var kinds []registry.MultiplexerKind

	for i, pane := range panes {
		kind := pane.Location.Kind
		if _, ok := byKind[kind]; !ok {
			kinds = append(kinds, kind)
		}

		byKind[kind] = append(byKind[kind], i)
	}

	for _, kind := range kinds {
		indexes := byKind[kind]

		driver, ok := find(kind)
		if !ok {
			for _, index := range indexes {
				captures[index].Err = errUnsupportedMultiplexerPane
			}

			continue
		}

		batch := make([]mux.Pane, len(indexes))
		for i, index := range indexes {
			batch[i] = panes[index]
		}

		results := driverCaptures(ctx, driver, batch)
		for i, index := range indexes {
			captures[index] = results[i]
			if results[i].Err != nil {
				captures[index] = mux.ScreenCapture{
					Snapshot: mux.ScreenSnapshot{Text: "", Title: ""},
					Err:      fmt.Errorf("capture %s pane: %w", kind, results[i].Err),
				}
			}
		}
	}

	return captures
}

func driverCaptures(ctx context.Context, driver mux.Driver, panes []mux.Pane) []mux.ScreenCapture {
	if batch, ok := driver.(mux.BatchCapturer); ok {
		return batch.CapturePanes(ctx, panes)
	}

	return captureEachPane(driver.CapturePane)(ctx, panes)
}

func captureEachPane(
	capture mux.ScreenCapturer,
) func(context.Context, []mux.Pane) []mux.ScreenCapture {
	return func(ctx context.Context, panes []mux.Pane) []mux.ScreenCapture {
		captures := make([]mux.ScreenCapture, len(panes))
		for i, pane := range panes {
			captures[i].Snapshot, captures[i].Err = capture(ctx, pane)
		}

		return captures
	}
}

func multiplexerPaneProcess(
	pane mux.Pane,
	processes []processinfo.Process,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
	paneCommandCounts map[string]int,
) (processinfo.Process, registry.Harness, bool) {
	if process, harnessID, ok := foregroundPaneProcess(pane, processes, harnessByPID); ok {
		return process, harnessID, true
	}

	for _, reference := range pane.Processes {
		if process, ok := tree.byPID[reference.PID]; ok {
			if harnessID, ok := harnessByPID[process.PID]; ok {
				return process, harnessID, true
			}
		}

		if process, harnessID, ok := descendantHarnessProcess(
			reference.PID,
			processes,
			tree,
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
	tree *processTree,
) bool {
	location := paneLocation(pane)
	if location.PaneTTY != "" && process.TTY == location.PaneTTY {
		return true
	}

	if process.PID == location.PanePID || tree.descendsFrom(process.PID, location.PanePID) {
		return true
	}

	for _, reference := range pane.Processes {
		if process.PID == reference.PID || tree.descendsFrom(process.PID, reference.PID) {
			return true
		}
	}

	return multiplexerIdentityMatches(process, location)
}

func normalizeHarnessForeground(
	processes []processinfo.Process,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) {
	for index, process := range processes {
		if !process.Foreground || harnessByPID[process.PID] == "" {
			continue
		}

		if nestedHarnessProcess(process, tree, harnessByPID) {
			// The kernel foreground flag covers a process group, including nested noninteractive agents.
			processes[index].Foreground = false
		}
	}

	for _, process := range processes {
		tree.byPID[process.PID] = process
	}
}

func nestedHarnessProcess(
	process processinfo.Process,
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) bool {
	for parent := range tree.parents(process.PID) {
		if _, ok := tree.byPID[parent]; ok && harnessByPID[parent] != "" {
			return true
		}
	}

	return false
}

func paneForegroundKnown(pane mux.Pane, processes []processinfo.Process) bool {
	tty := paneLocation(pane).PaneTTY
	if tty == "" {
		return false
	}

	for _, process := range processes {
		if process.TTY == tty && (process.Foreground || process.ProcessGroupID > 0) {
			return true
		}
	}

	return false
}

func (o *Observer) foregroundCandidate(
	pane mux.Pane,
	process processinfo.Process,
	processes []processinfo.Process,
) bool {
	if o.disableScreenInspection && pane.Activity == nil {
		return false
	}

	if process.Foreground {
		return true
	}

	if process.ProcessGroupID > 0 && process.TTY != "" {
		return false
	}

	return !paneForegroundKnown(pane, processes)
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
	driver, ok := drivers.Find(location.Kind)

	return ok && driver.SamePane(drivers.LocationFromEnv(process.Env), location)
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
	tree *processTree,
	harnessByPID map[int]registry.Harness,
) (processinfo.Process, registry.Harness, bool) {
	if rootPID <= 0 {
		var empty processinfo.Process
		return empty, "", false
	}

	for _, process := range processes {
		if harnessID, ok := harnessByPID[process.PID]; ok &&
			tree.descendsFrom(process.PID, rootPID) {
			return process, harnessID, true
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
