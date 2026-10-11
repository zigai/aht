package launch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const (
	strayTermGrace = 3 * time.Second
	strayKillGrace = 2 * time.Second
)

var errStraysSurvived = errors.New("launched processes survived SIGKILL")

func (h *Harness) stopStrays() error {
	marker := []byte("AHT_STORE=" + h.Store)
	pids := launchProcesses(marker)
	if len(pids) == 0 {
		return nil
	}
	signalAll(pids, syscall.SIGTERM)
	if waitGone(marker, strayTermGrace) {
		return nil
	}
	signalAll(launchProcesses(marker), syscall.SIGKILL)
	if waitGone(marker, strayKillGrace) {
		return nil
	}
	return fmt.Errorf("%w: %v", errStraysSurvived, launchProcesses(marker))
}

func launchProcesses(marker []byte) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		for variable := range bytes.SplitSeq(environ, []byte{0}) {
			if bytes.Equal(variable, marker) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

func signalAll(pids []int, signal syscall.Signal) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, signal)
	}
}

func waitGone(marker []byte, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if len(launchProcesses(marker)) == 0 {
			return true
		}
		time.Sleep(pollInterval)
	}
	return len(launchProcesses(marker)) == 0
}
