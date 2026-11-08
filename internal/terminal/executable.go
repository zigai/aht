package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var ErrNotInstalled = errors.New("tmux is not installed")

// FindExecutable bypasses personal PATH wrappers. AHT_TEST_TMUX_EXECUTABLE
// selects a different installed tmux binary.
func FindExecutable() (string, error) {
	if override := os.Getenv("AHT_TEST_TMUX_EXECUTABLE"); override != "" {
		path, err := exec.LookPath(override)
		if err != nil {
			return "", fmt.Errorf("resolve AHT_TEST_TMUX_EXECUTABLE: %w", err)
		}

		return path, nil
	}

	for _, candidate := range []string{"/usr/local/bin/tmux", "/opt/homebrew/bin/tmux", "/usr/bin/tmux", "tmux"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", ErrNotInstalled
}
