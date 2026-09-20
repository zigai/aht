package tmux

import (
	"context"
	"errors"
	"os/exec"

	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

var (
	_ mux.Driver      = (*Driver)(nil)
	_ mux.Interrupter = (*Driver)(nil)
)

// Driver implements [mux.Driver] and [mux.Interrupter] for tmux.
type Driver struct {
	LookPath func(string) (string, error)
}

// NewDriver returns a tmux driver.
func NewDriver() Driver {
	return Driver{LookPath: nil}
}

// Kind returns [registry.MultiplexerTmux].
func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerTmux
}

// Current returns the enclosing tmux context for the caller.
// If the caller is not inside tmux, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.MultiplexerContext, error) {
	tmuxCtx, err := Current(ctx)
	if err != nil {
		var empty registry.MultiplexerContext
		if errors.Is(err, ErrNoTmuxContext) {
			return empty, nil
		}
		return empty, err
	}
	return registry.MultiplexerFromTmux(tmuxCtx), nil
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
	panes, err := ListPanes(ctx)
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
		Tmux:           pane.Location.TmuxContext(),
		ServerIdentity: pane.Location.ServerID,
		PanePID:        pane.Location.PanePID,
		PaneTTY:        pane.Location.PaneTTY,
	}
	return CapturePane(ctx, tmuxPane)
}

// SendInterrupt sends an interrupt signal to the target pane.
func (d Driver) SendInterrupt(ctx context.Context, serverIdentity, paneID string) error {
	return SendInterruptTo(ctx, serverIdentity, paneID)
}
