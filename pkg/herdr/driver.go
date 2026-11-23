package herdr

import (
	"context"
	"fmt"
	"os"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var _ mux.Driver = (*Driver)(nil)

type Driver struct {
	ListOptions ListOptions
}

func NewDriver() Driver {
	return Driver{}
}

func (d Driver) Kind() registry.MultiplexerKind {
	return registry.MultiplexerHerdr
}

// Current returns the enclosing Herdr context for the caller.
// If the caller is not inside Herdr, it returns an empty context and nil error.
func (d Driver) Current(ctx context.Context) (registry.Location, error) {
	if err := ctx.Err(); err != nil {
		var empty registry.Location
		return empty, fmt.Errorf("current herdr context: %w", err)
	}

	return locationFromEnv(os.Getenv), nil
}

func (d Driver) EnvKeys() []string {
	return []string{envEnabled, envSessionName, envSocketPath, envWorkspaceID, envTabID, envPaneID}
}

func (d Driver) LocationFromEnv(env func(string) string) registry.Location {
	return locationFromEnv(env)
}

// ListPanes enumerates live Herdr panes. Herdr reports pane processes itself,
// so processes is unused.
func (d Driver) ListPanes(ctx context.Context, _ []mux.Process) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, d.ListOptions)
}

func (d Driver) CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePaneWithOptions(ctx, pane, CaptureOptions{Run: d.ListOptions.Run})
}

// Presence is always unknown: Herdr has no single-pane liveness query.
func (d Driver) Presence(context.Context, registry.Location) (registry.Presence, error) {
	return registry.PresenceUnknown, nil
}

// SamePane matches pane IDs by socket path when both paths are known.
func (d Driver) SamePane(a registry.Location, b registry.Location) bool {
	if a.Kind != registry.MultiplexerHerdr || b.Kind != registry.MultiplexerHerdr ||
		a.PaneID == "" || a.PaneID != b.PaneID {
		return false
	}

	if a.ServerID != "" && b.ServerID != "" {
		return registry.PathsEqual(a.ServerID, b.ServerID)
	}

	return a.SessionName != "" && a.SessionName == b.SessionName
}
