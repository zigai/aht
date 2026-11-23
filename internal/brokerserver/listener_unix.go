package brokerserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const staleSocketProbeTimeout = 100 * time.Millisecond

func listenLocal(ctx context.Context, path string) (net.Listener, os.FileInfo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("creating broker directory: %w", err)
	}

	listener, err := new(net.ListenConfig).Listen(ctx, "unix", path)
	if err != nil && errors.Is(err, syscall.EADDRINUSE) {
		probeCtx, cancel := context.WithTimeout(ctx, staleSocketProbeTimeout)
		connection, dialErr := new(net.Dialer).DialContext(probeCtx, "unix", path)

		cancel()

		if dialErr == nil {
			_ = connection.Close()
			return nil, nil, ErrAlreadyRunning
		}

		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, nil, fmt.Errorf(
				"%w: existing broker socket probe failed: %w",
				ErrAlreadyRunning,
				dialErr,
			)
		}

		if removeErr := removeStaleSocket(path); removeErr != nil {
			return nil, nil, removeErr
		}

		listener, err = new(net.ListenConfig).Listen(ctx, "unix", path)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("listening on broker socket %s: %w", path, err)
	}

	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)

		return nil, nil, fmt.Errorf("setting broker socket permissions: %w", err)
	}

	boundInfo, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)

		return nil, nil, fmt.Errorf("inspecting bound broker socket: %w", err)
	}

	return listener, boundInfo, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("inspecting stale broker socket: %w", err)
	}

	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: %s", errPathNotSocket, path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing stale broker socket: %w", err)
	}

	return nil
}

func cleanupLocal(path string, listener net.Listener, boundInfo os.FileInfo) {
	_ = listener.Close()

	if boundInfo == nil {
		return
	}

	current, err := os.Lstat(path)
	if err == nil && os.SameFile(boundInfo, current) {
		_ = os.Remove(path)
	}
}
