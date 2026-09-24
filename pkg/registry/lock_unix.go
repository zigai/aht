//go:build linux || darwin

package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const storeLockRetryInterval = 10 * time.Millisecond

type storeLock struct {
	file *os.File
}

func openStoreLock(ctx context.Context, path string, onContention ...func()) (*storeLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening store lock: %w", err)
	}
	var cb func()
	if len(onContention) > 0 {
		cb = onContention[0]
	}
	if err := acquireStoreLock(ctx, file, cb); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("locking store: %w", err),
				fmt.Errorf("closing store lock: %w", closeErr),
			)
		}

		return nil, fmt.Errorf("locking store: %w", err)
	}

	return &storeLock{file: file}, nil
}

func acquireStoreLock(ctx context.Context, file *os.File, onContention func()) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for store lock: %w", err)
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("acquiring file lock: %w", err)
		}
		if onContention != nil {
			onContention()
			onContention = nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for store lock: %w", ctx.Err())
		case <-time.After(storeLockRetryInterval):
		}
	}
}

func (l *storeLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlocking store: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing store lock: %w", closeErr)
	}

	return nil
}

func tryStoreLock(path string) (*storeLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening owner lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if closeErr != nil {
				return nil, fmt.Errorf("closing contended owner lock: %w", closeErr)
			}
			return nil, ErrStoreOwned
		}
		return nil, errors.Join(fmt.Errorf("locking owner: %w", err), closeErr)
	}
	return &storeLock{file: file}, nil
}
