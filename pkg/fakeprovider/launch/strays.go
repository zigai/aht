package launch

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

const (
	strayTermGrace = 3 * time.Second
	strayKillGrace = 2 * time.Second
)

var errStraysSurvived = errors.New("launched processes survived SIGKILL")

func (h *Harness) stopStrays(ctx context.Context) error {
	marker := []byte("AHT_STORE=" + h.Store)

	pids, err := launchProcesses(ctx, marker)
	if err != nil || len(pids) == 0 {
		return err
	}

	signalAll(pids, syscall.SIGTERM)

	if gone, err := waitGone(ctx, marker, strayTermGrace); err != nil || gone {
		return err
	}

	pids, err = launchProcesses(ctx, marker)
	if err != nil {
		return err
	}

	signalAll(pids, syscall.SIGKILL)

	if gone, err := waitGone(ctx, marker, strayKillGrace); err != nil || gone {
		return err
	}

	pids, err = launchProcesses(ctx, marker)
	if err != nil {
		return err
	}

	return fmt.Errorf("%w: %v", errStraysSurvived, pids)
}

func signalAll(pids []int, signal syscall.Signal) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, signal)
	}
}

func waitGone(ctx context.Context, marker []byte, grace time.Duration) (bool, error) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		pids, err := launchProcesses(ctx, marker)
		if err != nil || len(pids) == 0 {
			return len(pids) == 0, err
		}

		time.Sleep(pollInterval)
	}

	pids, err := launchProcesses(ctx, marker)

	return len(pids) == 0, err
}
