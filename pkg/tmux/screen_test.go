package tmux

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
)

func TestCapturePaneRequiresPaneID(t *testing.T) {
	t.Parallel()

	pane := Pane{Tmux: registry.TmuxContext{Inside: true, PaneID: ""}, ServerIdentity: "default", PanePID: 0, PaneTTY: ""}
	_, err := CapturePane(context.Background(), pane)
	if !errors.Is(err, errMissingCapturePane) {
		t.Fatalf("CapturePane with empty pane ID error = %v, want errMissingCapturePane", err)
	}
}

func TestCapturePaneRejectsInvalidServerIdentity(t *testing.T) {
	t.Parallel()

	pane := Pane{Tmux: registry.TmuxContext{Inside: true, PaneID: "%1"}, ServerIdentity: "-L:", PanePID: 0, PaneTTY: ""}
	_, err := CapturePane(context.Background(), pane)
	if !errors.Is(err, errInvalidServerIdentity) {
		t.Fatalf("CapturePane with invalid server identity error = %v, want errInvalidServerIdentity", err)
	}
}

func TestBoundBottomLinesPreservesBlankRows(t *testing.T) {
	t.Parallel()

	input := "row 1\n\nrow 3\n\n"
	got := mux.BoundBottomLines(input, 3)
	want := []string{"", "row 3", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BoundBottomLines = %#v, want %#v", got, want)
	}
}
