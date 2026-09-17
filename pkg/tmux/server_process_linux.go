//go:build linux

package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

var (
	errNotTmuxServer   = errors.New("process is not a tmux server")
	errEffectiveUserID = errors.New("effective user id outside uint32 range")
)

func listCurrentUserTmuxServers(ctx context.Context) ([]ServerProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("tmux process context: %w", err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}
	euid := os.Geteuid()
	if euid < 0 || euid > int(^uint32(0)) {
		return nil, fmt.Errorf("%d: %w", euid, errEffectiveUserID)
	}
	uid := uint32(euid)
	processes := make([]ServerProcess, 0)
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 0 {
			continue
		}
		process, processErr := readLinuxServerProcess(pid, uid)
		if processErr != nil {
			if errors.Is(processErr, errNotTmuxServer) || errors.Is(processErr, os.ErrNotExist) || errors.Is(processErr, os.ErrPermission) {
				continue
			}
			return nil, processErr
		}
		processes = append(processes, *process)
	}
	return processes, nil
}

func readLinuxServerProcess(pid int, uid uint32) (*ServerProcess, error) {
	info, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return nil, fmt.Errorf("stat tmux process: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uid {
		return nil, errNotTmuxServer
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, fmt.Errorf("read tmux command line: %w", err)
	}
	trimmed := strings.TrimRight(string(data), "\x00")
	if trimmed == "" {
		return nil, errNotTmuxServer
	}
	args := strings.Split(trimmed, "\x00")
	commData, commErr := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if commErr == nil && isTmuxServerComm(strings.TrimSpace(string(commData))) {
		return &ServerProcess{PID: pid, Args: args}, nil
	}
	if !isTmuxServerArgs(args) {
		return nil, errNotTmuxServer
	}
	return &ServerProcess{PID: pid, Args: args}, nil
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
		if index != 0 || !isTmuxBinaryName(base) {
			continue
		}
		if slices.Contains(args[1:], "server") || slices.Contains(args[1:], "-D") || slices.Contains(args[1:], "-d") {
			return true
		}
	}
	return false
}

func isTmuxServerComm(comm string) bool {
	if strings.HasPrefix(comm, "tmux: server") {
		return true
	}
	if strings.HasPrefix(comm, "tmux") && (strings.Contains(comm, "serve") || strings.HasSuffix(comm, ":")) {
		return true
	}
	return false
}
