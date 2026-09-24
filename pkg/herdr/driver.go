package herdr

import (
	"context"
	"fmt"

	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

var _ mux.Driver = (*Driver)(nil)

// Driver implements [mux.Driver] for Herdr.
type Driver struct {
	ListOptions ListOptions
}

// NewDriver returns a Herdr driver.
func NewDriver() Driver {
	return Driver{ListOptions: ListOptions{Run: nil, LookPath: nil}}
}

// Kind returns [registry.MultiplexerHerdr].
func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerHerdr
}

// Current returns the enclosing Herdr context for the caller.
// If the caller is not inside Herdr, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.Location, error) {
	if err := ctx.Err(); err != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, fmt.Errorf("current herdr context: %w", err)
	}
	return Current(), nil
}

// ListPanes enumerates live Herdr panes.
func (d Driver) ListPanes(ctx context.Context) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, d.ListOptions)
}

// CapturePane captures the screen text and title for pane.
func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePane(ctx, pane)
}
