package zellij

import (
	"context"
	"fmt"
	"os"

	"github.com/zigai/aht/v2/internal/processinfo"
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
	return Driver{}
}

// Kind returns [registry.MultiplexerZellij].
func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerZellij
}

// Current returns the enclosing Zellij context for the caller.
// If the caller is not inside Zellij, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.Location, error) {
	if err := ctx.Err(); err != nil {
		var empty registry.Location
		return empty, fmt.Errorf("current zellij context: %w", err)
	}

	return locationFromEnv(os.Getenv), nil
}

func (d Driver) EnvKeys() []string { return []string{envSessionName, envPaneID} }

func (d Driver) LocationFromEnv(env func(string) string) registry.Location {
	return locationFromEnv(env)
}

// ListPanes enumerates live Zellij panes. Zellij reports panes without
// process inspection, so processes is unused.
func (d Driver) ListPanes(ctx context.Context, _ []processinfo.Process) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, d.ListOptions)
}

func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePaneWithOptions(
		ctx,
		pane,
		CaptureOptions{Run: d.ListOptions.Run, SocketDir: d.ListOptions.SocketDir},
	)
}

// Presence is always unknown: Zellij has no single-pane liveness query.
func (d Driver) Presence(context.Context, registry.Location) (registry.Presence, error) {
	return registry.PresenceUnknown, nil
}

// SamePane matches pane ids within one session; Zellij pane ids repeat across
// sessions.
func (d Driver) SamePane(a registry.Location, b registry.Location) bool {
	return a.Kind == registry.MultiplexerZellij && b.Kind == registry.MultiplexerZellij &&
		a.PaneID != "" && a.PaneID == b.PaneID &&
		a.SessionName != "" && a.SessionName == b.SessionName
}
