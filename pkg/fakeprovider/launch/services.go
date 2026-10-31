package launch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/broker"
)

const pollInterval = 20 * time.Millisecond

// StartTracker starts the AHT tracker for this launch.
func (h *Harness) StartTracker(ctx context.Context) error {
	if h.aht == "" {
		return errNoAHT
	}

	if h.tracker != nil {
		return errTrackerRun
	}

	p, err := h.startProcess(
		ctx,
		"tracker",
		h.aht,
		"--store",
		h.Store,
		"manage",
		"tracker",
		"run",
		"--quiet",
	)
	if err != nil {
		return err
	}

	h.tracker = p
	if err := waitForSocket(ctx, p, h.trackerSocket()); err != nil {
		return errors.Join(err, h.stopTracker())
	}

	return nil
}

func (h *Harness) startService(
	ctx context.Context,
	index int,
	service harness.LaunchService,
) error {
	name := "service-" + strconv.Itoa(index)

	if service.Address != "" {
		if _, _, err := net.SplitHostPort(service.Address); err != nil {
			return fmt.Errorf("%s address %q: %w", name, service.Address, err)
		}
	}

	p, err := h.startProcess(ctx, name, h.Executable, service.Args...)
	if err != nil {
		return err
	}

	h.services = append(h.services, p)

	if service.Address == "" {
		return nil
	}

	return waitForAddress(ctx, p, service.Address)
}

func (h *Harness) stopTracker() error {
	if h.tracker == nil {
		return nil
	}

	err := h.tracker.stop()
	h.tracker = nil

	return err
}

func (h *Harness) trackerSocket() string {
	if socket, ok := envValue(h.Env, broker.SocketPathEnv); ok {
		return socket
	}

	return filepath.Clean(h.Store) + ".sock"
}

func waitForAddress(ctx context.Context, p *process, address string) error {
	var dialer net.Dialer

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			if err := conn.Close(); err != nil {
				return fmt.Errorf("close probe connection to %s: %w", address, err)
			}

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"wait for %s on %s (log %s): %w",
				p.name,
				address,
				p.logPath,
				ctx.Err(),
			)
		case <-p.done:
			return fmt.Errorf(
				"%s (log %s): %w",
				p.name,
				p.logPath,
				errors.Join(errExitedEarly, p.waitErr),
			)
		case <-ticker.C:
		}
	}
}

func waitForSocket(ctx context.Context, p *process, path string) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"wait for %s socket %s (log %s): %w",
				p.name,
				path,
				p.logPath,
				ctx.Err(),
			)
		case <-p.done:
			return fmt.Errorf(
				"%s (log %s): %w",
				p.name,
				p.logPath,
				errors.Join(errExitedEarly, p.waitErr),
			)
		case <-ticker.C:
		}
	}
}
