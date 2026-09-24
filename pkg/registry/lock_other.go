//go:build !linux && !darwin

package registry

import (
	"context"
	"fmt"
	"os"
)

type storeLock struct {
	file *os.File
	path string
}

func openStoreLock(ctx context.Context, path string, _ ...func()) (*storeLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("opening store lock: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening store lock: %w", err)
	}
	return &storeLock{file: file, path: path}, nil
}

func (l *storeLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	closeErr := l.file.Close()
	removeErr := os.Remove(l.path)
	if closeErr != nil {
		if removeErr != nil {
			return fmt.Errorf("closing store lock: %w; removing store lock: %v", closeErr, removeErr)
		}
		return fmt.Errorf("closing store lock: %w", closeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("removing store lock: %w", removeErr)
	}
	return nil
}

func tryStoreLock(path string) (*storeLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if os.IsExist(err) {
		return nil, ErrStoreOwned
	}
	if err != nil {
		return nil, fmt.Errorf("opening owner lock: %w", err)
	}
	return &storeLock{file: file, path: path}, nil
}
