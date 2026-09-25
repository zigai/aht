package zellij

import (
	"context"
	"fmt"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var _ mux.Driver = (*Driver)(nil)

// Driver implements [mux.Driver] for Zellij.
type Driver struct {
	ListOptions ListOptions
}

// NewDriver returns a Zellij driver.
func NewDriver() Driver {
	return Driver{ListOptions: ListOptions{Run: nil, LookPath: nil}}
}

// Kind returns [registry.MultiplexerZellij].
func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerZellij
}

// Current returns the enclosing Zellij context for the caller.
// If the caller is not inside Zellij, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.Location, error) {
	if err := ctx.Err(); err != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, fmt.Errorf("current zellij context: %w", err)
	}
	return Current(), nil
}

// ListPanes enumerates live Zellij panes.
func (d Driver) ListPanes(ctx context.Context) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, d.ListOptions)
}

// CapturePane captures the screen text and title for pane.
func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePane(ctx, pane)
}
