//go:build !linux && !darwin

package claude

import (
	"errors"
	"fmt"
	"os"

	"github.com/zigai/aht/v2/internal/harness/titlefile"
)

func openTitleSource(path string) (*os.File, os.FileInfo, error) {
	file, err := titlefile.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open title file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	return file, info, nil
}

func stampTitleSource(_ os.FileInfo) (titleSourceStamp, error) {
	return titleSourceStamp{}, fmt.Errorf("inspect Claude transcript identity on this platform: %w", errors.ErrUnsupported)
}
