//go:build linux || darwin

package observer

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openObserverLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening observer lock: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("locking observer: %w", err), file.Close())
	}

	return file, nil
}

func closeObserverLock(file *os.File) error {
	if file == nil {
		return nil
	}

	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()

	if unlockErr != nil {
		unlockErr = fmt.Errorf("unlocking observer: %w", unlockErr)
	}

	if closeErr != nil {
		closeErr = fmt.Errorf("closing observer lock: %w", closeErr)
	}

	return errors.Join(unlockErr, closeErr)
}
