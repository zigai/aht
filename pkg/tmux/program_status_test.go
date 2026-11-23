package tmux

import (
	"context"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/mux"
)

func TestAwaitProgramStatusWakesDuringSnapshotDelivery(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	signals := make(chan struct{})
	wait := func(ctx context.Context) error {
		select {
		case <-signals:
		case <-ctx.Done():
		}

		return nil
	}

	read := func(context.Context) ([]mux.Pane, error) { return nil, nil }

	var delivered int

	changed := func([]mux.Pane) {
		delivered++
		if delivered == 1 {
			select {
			case signals <- struct{}{}:
			case <-ctx.Done():
			}
		} else {
			cancel()
		}
	}

	if err := awaitProgramStatus(ctx, wait, read, changed); err != nil || delivered != 2 {
		t.Fatalf("delivered %d snapshots, error %v; want two snapshots", delivered, err)
	}
}
