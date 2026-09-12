package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/pkg/mux"
)

const defaultCaptureLines = 100

var (
	errMissingCapturePane    = errors.New("capture pane id is required")
	errInvalidServerIdentity = errors.New("invalid tmux server identity")
)

type ScreenSnapshot struct {
	Text  string
	Title string
}

type CaptureOptions struct {
	Lines int
}

func CapturePane(ctx context.Context, pane Pane) (ScreenSnapshot, error) {
	return CapturePaneWithOptions(ctx, pane, CaptureOptions{Lines: 0})
}

func CapturePaneWithOptions(ctx context.Context, pane Pane, options CaptureOptions) (ScreenSnapshot, error) {
	if strings.TrimSpace(pane.Tmux.PaneID) == "" {
		return ScreenSnapshot{}, errMissingCapturePane
	}
	cfg, err := gotmuxConfigForIdentity(pane.ServerIdentity)
	if err != nil {
		return ScreenSnapshot{}, err
	}
	server, err := gotmux.New(cfg)
	if err != nil {
		return ScreenSnapshot{}, fmt.Errorf("init tmux client: %w", err)
	}
	paneHandle, err := server.PaneHandle(gotmux.PaneID(pane.Tmux.PaneID))
	if err != nil {
		return ScreenSnapshot{}, fmt.Errorf("resolve tmux pane %s: %w", pane.Tmux.PaneID, err)
	}
	lines := min(options.Lines, defaultCaptureLines)
	captureOpts := gotmux.CaptureOptions{ //nolint:exhaustruct_v5 // remaining options default to empty
		JoinWrapped:    true,
		IncludeEscapes: true,
	}
	if lines > 0 {
		start := -lines
		captureOpts.Start = &start
	}
	res, err := paneHandle.CaptureWithTitle(ctx, captureOpts)
	if err != nil {
		return ScreenSnapshot{}, fmt.Errorf("capturing pane %s: %w", pane.Tmux.PaneID, err)
	}
	text := string(res.Output)
	if lines > 0 {
		text = strings.Join(mux.BoundBottomLines(text, lines), "\n")
	}
	return ScreenSnapshot{Text: text, Title: strings.TrimRight(res.Title, "\r\n")}, nil
}

func gotmuxConfigForIdentity(identity string) (gotmux.Config, error) {
	identity = strings.TrimSpace(identity)
	switch {
	case identity == "", identity == "default":
		return gotmux.Config{}, nil //nolint:exhaustruct_v5 // zero values for default endpoint
	case strings.HasPrefix(identity, "-L:"):
		name := strings.TrimPrefix(identity, "-L:")
		if name == "" {
			return gotmux.Config{}, fmt.Errorf("%w: %q", errInvalidServerIdentity, identity)
		}
		return gotmux.Config{SocketName: name}, nil //nolint:exhaustruct_v5 // zero values for named socket
	default:
		return gotmux.Config{SocketPath: identity}, nil //nolint:exhaustruct_v5 // zero values for path socket
	}
}
