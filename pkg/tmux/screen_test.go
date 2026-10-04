package tmux

import (
	"context"
	"errors"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestCapturePaneRequiresPaneID(t *testing.T) {
	t.Parallel()

	pane := Pane{Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "default", PaneID: ""}, PanePID: 0, PaneTTY: ""}
	_, err := CapturePane(context.Background(), pane)
	if !errors.Is(err, errMissingCapturePane) {
		t.Fatalf("CapturePane with empty pane ID error = %v, want errMissingCapturePane", err)
	}
}

func TestCapturePaneRejectsInvalidServerIdentity(t *testing.T) {
	t.Parallel()

	pane := Pane{Location: registry.Location{Kind: registry.MultiplexerTmux, ServerID: "-L:", PaneID: "%1"}, PanePID: 0, PaneTTY: ""}
	_, err := CapturePane(context.Background(), pane)
	if !errors.Is(err, errInvalidServerIdentity) {
		t.Fatalf("CapturePane with invalid server identity error = %v, want errInvalidServerIdentity", err)
	}
}
