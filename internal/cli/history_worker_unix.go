//go:build linux || darwin

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func detachHistoryCommand(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func lockHistoryRefresh() (*os.File, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate history refresh guard: %w", err)
	}
	path := filepath.Join(cache, "aht", "history-v1.sqlite.refresh.lock")
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create history refresh guard directory: %w", err)
	}
	guard, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open history refresh guard: %w", err)
	}
	if err = unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = guard.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errHistoryRefreshRunning
		}
		return nil, fmt.Errorf("lock history refresh guard: %w", err)
	}
	return guard, nil
}
