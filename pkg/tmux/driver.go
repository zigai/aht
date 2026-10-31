package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	_ mux.Driver      = (*Driver)(nil)
	_ mux.Interrupter = (*Driver)(nil)
)

// Driver implements [mux.Driver] and [mux.Interrupter] for tmux.
type Driver struct {
	LookPath func(string) (string, error)
	// ServerProcesses, when non-nil, replaces the process scan that finds tmux
	// servers outside the standard socket directories.
	ServerProcesses ServerProcessLister
}

// NewDriver returns a tmux driver.
func NewDriver() Driver {
	return Driver{LookPath: nil, ServerProcesses: nil}
}

// Kind returns [registry.MultiplexerTmux].
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

// ListPanes enumerates live tmux panes converted to [mux.Pane].
// If tmux is not installed, it returns nil, nil.
func (d Driver) ListPanes(ctx context.Context) ([]mux.Pane, error) {
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("tmux"); err != nil {
		//nolint:nilerr // an optional unavailable multiplexer contributes no panes
		return nil, nil
	}
	panes, err := ListPanesWithOptions(ctx, ListOptions{
		Env:             Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")},
		ServerProcesses: d.ServerProcesses,
		SocketPaths:     nil,
	})
	if err != nil {
		return nil, err
	}
	result := make([]mux.Pane, 0, len(panes))
	for _, p := range panes {
		result = append(result, p.ToMuxPane())
	}
	return result, nil
}

// CapturePane captures the screen text and title for pane.
func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	tmuxPane := Pane{
		Location: pane.Location,
		PanePID:  pane.Location.PanePID,
		PaneTTY:  pane.Location.PaneTTY,
	}
	return CapturePane(ctx, tmuxPane)
}

// SendInterrupt sends an interrupt signal to the target pane.
func (d Driver) SendInterrupt(ctx context.Context, serverIdentity, paneID string) error {
	return SendInterruptTo(ctx, serverIdentity, paneID)
}
