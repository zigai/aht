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
	capture := CapturePanes(ctx, []Pane{pane})[0]

	return capture.Snapshot, capture.Err
}

// CapturePanes captures the given panes with one tmux call per server. The
// result holds one capture per pane, in the given order.
func CapturePanes(ctx context.Context, panes []Pane) []mux.ScreenCapture {
	captures := make([]mux.ScreenCapture, len(panes))
	byServer := make(map[string][]int)

	var servers []string

	for i, pane := range panes {
		if strings.TrimSpace(pane.Location.PaneID) == "" {
			captures[i].Err = errMissingCapturePane

			continue
		}

		identity := strings.TrimSpace(pane.Location.ServerID)
		if _, ok := byServer[identity]; !ok {
			servers = append(servers, identity)
		}

		byServer[identity] = append(byServer[identity], i)
	}

	for _, identity := range servers {
		captureServerPanes(ctx, identity, panes, byServer[identity], captures)
	}

	return captures
}

func captureServerPanes(
	ctx context.Context,
	identity string,
	panes []Pane,
	indexes []int,
	captures []mux.ScreenCapture,
) {
	server, err := captureServer(identity)
	if err != nil {
		for _, index := range indexes {
			captures[index].Err = err
		}

		return
	}

	handles := make([]gotmux.Pane, 0, len(indexes))
	captured := make([]int, 0, len(indexes))

	for _, index := range indexes {
		paneID := panes[index].Location.PaneID

		handle, err := server.PaneHandle(gotmux.PaneID(paneID))
		if err != nil {
			captures[index].Err = fmt.Errorf("resolve tmux pane %s: %w", paneID, err)

			continue
		}

		handles = append(handles, handle)
		captured = append(captured, index)
	}

	results, err := server.CapturePanes(ctx, handles, gotmux.CaptureOptions{
		JoinWrapped:    true,
		IncludeEscapes: true,
	})

	for i, index := range captured {
		paneID := panes[index].Location.PaneID

		switch {
		case err != nil:
			captures[index].Err = fmt.Errorf("capturing pane %s: %w", paneID, err)
		case results[i].Err != nil:
			captures[index].Err = fmt.Errorf("capturing pane %s: %w", paneID, results[i].Err)
		default:
			captures[index].Snapshot = mux.ScreenSnapshot{
				Text:  string(results[i].Output),
				Title: strings.TrimRight(results[i].Title, "\r\n"),
			}
		}
	}
}

func captureServer(identity string) (*gotmux.Server, error) {
	cfg, err := gotmuxConfigForIdentity(identity)
	if err != nil {
		return nil, err
	}

	server, err := gotmux.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("init tmux client: %w", err)
	}

	return server, nil
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
