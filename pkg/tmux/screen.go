package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/pkg/mux"
)

var (
	errMissingCapturePane    = errors.New("capture pane id is required")
	errInvalidServerIdentity = errors.New("invalid tmux server identity")
)

func CapturePane(ctx context.Context, pane Pane) (mux.ScreenSnapshot, error) {
	if strings.TrimSpace(pane.Location.PaneID) == "" {
		return mux.ScreenSnapshot{}, errMissingCapturePane
	}

	cfg, err := gotmuxConfigForIdentity(pane.Location.ServerID)
	if err != nil {
		return mux.ScreenSnapshot{}, err
	}

	server, err := gotmux.New(cfg)
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf("init tmux client: %w", err)
	}

	paneHandle, err := server.PaneHandle(gotmux.PaneID(pane.Location.PaneID))
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf(
			"resolve tmux pane %s: %w",
			pane.Location.PaneID,
			err,
		)
	}

	captureOpts := gotmux.CaptureOptions{
		JoinWrapped:    true,
		IncludeEscapes: true,
	}

	res, err := paneHandle.CaptureWithTitle(ctx, captureOpts)
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf("capturing pane %s: %w", pane.Location.PaneID, err)
	}

	text := string(res.Output)

	return mux.ScreenSnapshot{Text: text, Title: strings.TrimRight(res.Title, "\r\n")}, nil
}

func gotmuxConfigForIdentity(identity string) (gotmux.Config, error) {
	identity = strings.TrimSpace(identity)
	switch {
	case identity == "", identity == "default":
		return gotmux.Config{}, nil
	case strings.HasPrefix(identity, "-L:"):
		name := strings.TrimPrefix(identity, "-L:")
		if name == "" {
			return gotmux.Config{}, fmt.Errorf("%w: %q", errInvalidServerIdentity, identity)
		}

		return gotmux.Config{SocketName: name}, nil
	default:
		return gotmux.Config{SocketPath: identity}, nil
	}
}
