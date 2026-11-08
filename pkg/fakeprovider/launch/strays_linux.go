package launch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// linuxProcFS is the kernel process interface, independent of temporary directories.
const linuxProcFS = "/proc"

func launchProcesses(_ context.Context, marker []byte) ([]int, error) {
	entries, err := os.ReadDir(linuxProcFS)
	if err != nil {
		return nil, fmt.Errorf("list launch processes: %w", err)
	}

	self := os.Getpid()

	var pids []int

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}

		environ, err := os.ReadFile(filepath.Join(linuxProcFS, entry.Name(), "environ"))
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

	return pids, nil
}
