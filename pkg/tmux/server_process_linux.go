//go:build linux

package tmux

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/zigai/aht/v2/internal/processinfo"
)

func isServerProcess(process processinfo.Process) bool {
	return process.PID > 0 && len(process.Args) > 0 &&
		(isTmuxServerComm(process.Name) || isTmuxServerArgs(process.Args))
}

func isTmuxServerArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}

	for index, arg := range args {
		base := filepath.Base(arg)
		if strings.HasPrefix(base, "tmux: server") {
			return true
		}

		if index != 0 || !isBinaryName(base) {
			continue
		}

		if slices.Contains(args[1:], "server") || slices.Contains(args[1:], "-D") ||
			slices.Contains(args[1:], "-d") {
			return true
		}
	}

	return false
}

func isTmuxServerComm(comm string) bool {
	if strings.HasPrefix(comm, "tmux: server") {
		return true
	}

	if strings.HasPrefix(comm, "tmux") &&
		(strings.Contains(comm, "serve") || strings.HasSuffix(comm, ":")) {
		return true
	}

	return false
}
