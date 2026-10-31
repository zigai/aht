//go:build darwin

package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ServerProcessFrom reports whether a current-user process, given its kernel
// command name and arguments, is a tmux server that discovery should query.
func ServerProcessFrom(pid int, name string, args []string) (ServerProcess, bool) {
	if pid <= 0 || !isDarwinTmuxCommand(name) {
		return ServerProcess{PID: 0, Args: nil}, false
	}
	return ServerProcess{PID: pid, Args: args}, true
}

func listCurrentUserTmuxServers(ctx context.Context) ([]ServerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list tmux servers: %w", err)
	}
	processTable, err := unix.SysctlKinfoProcSlice("kern.proc.uid", unix.Getuid())
	if err != nil {
		return nil, fmt.Errorf("listing tmux processes: %w", err)
	}
	processes := make([]ServerProcess, 0)
	for _, process := range processTable {
		server, found, err := darwinTmuxServer(ctx, process)
		if err != nil {
			return nil, err
		}
		if found {
			processes = append(processes, server)
		}
	}
	return processes, nil
}

func darwinTmuxServer(ctx context.Context, process unix.KinfoProc) (ServerProcess, bool, error) {
	var zero ServerProcess
	if err := ctx.Err(); err != nil {
		return zero, false, fmt.Errorf("inspect tmux server: %w", err)
	}
	pid := int(process.Proc.P_pid)
	command := darwinProcessCommand(process.Proc.P_comm[:])
	if pid <= 0 || !isDarwinTmuxCommand(command) {
		return zero, false, nil
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.EINVAL) {
			return zero, false, nil
		}
		return zero, false, fmt.Errorf("reading tmux process %d arguments: %w", pid, err)
	}
	args, err := parseDarwinProcArgs(data)
	if err != nil {
		return zero, false, fmt.Errorf("parsing tmux process %d arguments: %w", pid, err)
	}
	return ServerProcess{PID: pid, Args: args}, true, nil
}

func isDarwinTmuxCommand(name string) bool {
	return strings.HasPrefix(filepath.Base(name), "tmux")
}

func darwinProcessCommand(data []byte) string {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		data = data[:index]
	}

	return string(data)
}
