package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	_ mux.Driver        = (*Driver)(nil)
	_ mux.Interrupter   = (*Driver)(nil)
	_ mux.BatchCapturer = (*Driver)(nil)
)

// Driver implements [mux.Driver], [mux.Interrupter] and [mux.BatchCapturer] for tmux.
type Driver struct {
	LookPath func(string) (string, error)
}

func NewDriver() Driver {
	return Driver{}
}

func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerTmux
}

// Current returns the enclosing tmux context for the caller.
// If the caller is not inside tmux, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.Location, error) {
	location, err := Current(ctx)
	if err != nil {
		var empty registry.Location
		if errors.Is(err, ErrNotInTmux) {
			return empty, nil
		}

		return empty, err
	}

	return location, nil
}

// EnvKeys is empty: tmux panes are tied to processes by TTY and pane PID, not
// by the process environment.
func (d Driver) EnvKeys() []string { return nil }

func (d Driver) LocationFromEnv(func(string) string) registry.Location {
	var empty registry.Location

	return empty
}

// ListPanes enumerates live tmux panes converted to [mux.Pane]. processes is
// searched for servers on custom sockets. If tmux is not installed, it returns
// nil, nil.
func (d Driver) ListPanes(
	ctx context.Context,
	processes []processinfo.Process,
) ([]mux.Pane, error) {
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	if _, err := lookPath("tmux"); err != nil {
		//nolint:nilerr // an optional unavailable multiplexer contributes no panes
		return nil, nil
	}

	panes, err := ListPanesWithOptions(ctx, ListOptions{
		Env:       Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")},
		Processes: processes,
	})

	result := make([]mux.Pane, 0, len(panes))
	for _, p := range panes {
		result = append(result, p.ToMuxPane())
	}

	return result, err
}

func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePane(ctx, capturedPane(pane))
}

func (d Driver) CapturePanes(ctx context.Context, panes []mux.Pane) []mux.ScreenCapture {
	tmuxPanes := make([]Pane, len(panes))
	for i, pane := range panes {
		tmuxPanes[i] = capturedPane(pane)
	}

	return CapturePanes(ctx, tmuxPanes)
}

func capturedPane(pane mux.Pane) Pane {
	return Pane{
		Location: pane.Location,
		PanePID:  pane.Location.PanePID,
		PaneTTY:  pane.Location.PaneTTY,
	}
}

func (d Driver) Presence(
	ctx context.Context,
	location registry.Location,
) (registry.Presence, error) {
	return PanePresence(ctx, location)
}

// SamePane matches pane ids within one server; tmux pane ids repeat across
// servers, and session names can change.
func (d Driver) SamePane(a registry.Location, b registry.Location) bool {
	return a.Kind == registry.MultiplexerTmux && b.Kind == registry.MultiplexerTmux &&
		a.PaneID != "" && a.PaneID == b.PaneID &&
		a.ServerID != "" && (a.ServerID == b.ServerID || registry.PathsEqual(a.ServerID, b.ServerID))
}

func (d Driver) SendInterrupt(ctx context.Context, serverIdentity string, paneID string) error {
	return SendInterruptTo(ctx, serverIdentity, paneID)
}
