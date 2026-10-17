//go:build !linux && !darwin

package cli

import (
	"errors"
	"os"
	"os/exec"
)

var errHistoryPriorityUnsupported = errors.New("background history refresh requires Linux or macOS")

func detachHistoryCommand(_ *exec.Cmd) error {
	return errHistoryPriorityUnsupported
}

func lockHistoryRefresh() (*os.File, error) {
	return nil, errHistoryPriorityUnsupported
}

func setHistoryPriority() error {
	return errHistoryPriorityUnsupported
}
