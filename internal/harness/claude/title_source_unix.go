//go:build linux || darwin

package claude

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openTitleSource(path string) (*os.File, os.FileInfo, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open title file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.Join(fmt.Errorf("title file %q is not regular: %w", path, os.ErrInvalid), file.Close())
	}
	return file, info, nil
}
