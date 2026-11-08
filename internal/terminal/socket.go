package terminal

import (
	"fmt"
	"os"
)

// NewSocketDir creates a short directory for a Unix socket. The caller owns cleanup.
func NewSocketDir(prefix string) (string, error) {
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		return "", fmt.Errorf("create socket directory: %w", err)
	}

	return dir, nil
}
