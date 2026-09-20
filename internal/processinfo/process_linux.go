//go:build linux

package processinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	procRoot  = "/proc"
	maxUint32 = int(^uint32(0))
)

var (
	errEmptyBootIdentity       = errors.New("empty boot identity")
	errInvalidEffectiveUserID  = errors.New("effective user id outside uint32 range")
	errMissingCommandDelimiter = errors.New("missing command delimiter")
	errInvalidProcessID        = errors.New("invalid process id")
	errTruncatedStatRecord     = errors.New("truncated stat record")
	errInvalidProcessState     = errors.New("invalid process state")
	errInvalidParentProcessID  = errors.New("invalid parent process id")
	errInvalidProcessGroupID   = errors.New("invalid process group id")
	errInvalidForegroundGroup  = errors.New("invalid foreground process group id")
	errInvalidProcessStartTime = errors.New("invalid process start time")
)

type linuxEnvironmentHints struct {
	agent, herdrPane, herdrServer, herdrSession, zellijPane, zellijSession string
}

// List returns a complete best-effort snapshot of processes owned by the
// effective user. Processes which exit while being read are skipped.
//
//nolint:gocognit,cyclop // process enumeration intentionally handles transient proc entries inline
func List(ctx context.Context) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("process listing context: %w", err)
	}

	boot, err := linuxBootIdentity()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, classifyProcessError(procRoot, err)
	}

	euid := os.Geteuid()
	if euid < 0 || euid > maxUint32 {
		return nil, fmt.Errorf("%d: %w", euid, errInvalidEffectiveUserID)
	}
	uid := uint32(euid)
	processes := make([]Process, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("process listing context: %w", err)
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}

		dir := filepath.Join(procRoot, entry.Name())
		info, err := os.Stat(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, classifyProcessError(dir, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uid {
			continue
		}

		process, err := readLinuxProcess(dir, pid, boot)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		processes = append(processes, process)
	}
	return processes, nil
}

// Find returns the current-user process identified by pid. A false found
// result means the process does not exist or belongs to another user.
func Find(ctx context.Context, pid int) (Process, bool, error) {
	var zero Process
	if err := ctx.Err(); err != nil {
		return zero, false, fmt.Errorf("finding process context: %w", err)
	}
	if pid <= 0 {
		return zero, false, nil
	}

	boot, err := linuxBootIdentity()
	if err != nil {
		return zero, false, err
	}
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, false, nil
		}
		return zero, false, classifyProcessError(dir, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return zero, false, nil
	}

	process, err := readLinuxProcess(dir, pid, boot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, false, nil
		}
		return zero, false, err
	}
	return process, true, nil
}

// StartIdentity returns a boot-qualified process start identity for pid.
func StartIdentity(ctx context.Context, pid int) string {
	if pid <= 0 || ctx.Err() != nil {
		return ""
	}
	bootID, err := os.ReadFile(filepath.Join(procRoot, "sys/kernel/random/boot_id"))
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("%s/%d/stat", procRoot, pid))
	if err != nil {
		return ""
	}
	start := linuxStartTimeFromStat(string(data))
	if start == "" {
		return ""
	}
	return strings.TrimSpace(string(bootID)) + ":" + start
}

// CommandName returns the executable command name for pid on Linux.
func CommandName(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("checking context: %w", err)
	}

	data, err := os.ReadFile(fmt.Sprintf("%s/%d/comm", procRoot, pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("reading process command: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func linuxBootIdentity() (string, error) {
	path := filepath.Join(procRoot, "sys/kernel/random/boot_id")
	bootID, err := os.ReadFile(path)
	if err != nil {
		return "", classifyProcessError(path, err)
	}
	boot := strings.TrimSpace(string(bootID))
	if boot == "" {
		return "", &TableError{Path: path, Err: errEmptyBootIdentity}
	}
	return boot, nil
}

func readLinuxProcess(dir string, pid int, boot string) (Process, error) {
	statPath := filepath.Join(dir, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Process{}, fmt.Errorf("read process stat: %w", err)
		}
		return Process{}, classifyProcessError(statPath, err)
	}
	info, err := parseLinuxStat(string(data))
	if err != nil {
		return Process{}, &TableError{Path: statPath, Err: err}
	}
	info.PID = pid
	info.StartIdentity = boot + ":" + info.StartIdentity
	info.Executable = readLinuxLink(filepath.Join(dir, "exe"))
	info.CWD = readLinuxLink(filepath.Join(dir, "cwd"))
	if tty := readLinuxLink(filepath.Join(dir, "fd", "0")); isLinuxTTY(tty) {
		info.TTY = tty
	}
	if args, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		info.Args = splitLinuxArgs(args)
	}
	if environ, err := os.ReadFile(filepath.Join(dir, "environ")); err == nil {
		hints := parseLinuxEnvironmentHints(environ)
		info.AgentHint = hints.agent
		switch {
		case hints.herdrPane != "":
			info.MultiplexerKind = "herdr"
			info.MultiplexerServer = hints.herdrServer
			info.MultiplexerSession = hints.herdrSession
			info.MultiplexerPane = hints.herdrPane
		case hints.zellijPane != "":
			info.MultiplexerKind = "zellij"
			info.MultiplexerSession = hints.zellijSession
			info.MultiplexerPane = hints.zellijPane
		}
	}
	return info, nil
}

func readLinuxLink(path string) string {
	link, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return link
}

func isLinuxTTY(path string) bool {
	return path == "/dev/tty" || strings.HasPrefix(path, "/dev/pts/") || strings.HasPrefix(path, "/dev/tty")
}

func parseLinuxEnvironmentHints(data []byte) linuxEnvironmentHints {
	var hints linuxEnvironmentHints
	remaining := map[string]*string{
		"AHT_AGENT":           &hints.agent,
		"HERDR_PANE_ID":       &hints.herdrPane,
		"HERDR_SOCKET_PATH":   &hints.herdrServer,
		"HERDR_SESSION":       &hints.herdrSession,
		"ZELLIJ_PANE_ID":      &hints.zellijPane,
		"ZELLIJ_SESSION_NAME": &hints.zellijSession,
	}
	for entry := range strings.SplitSeq(string(data), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if target := remaining[key]; ok && target != nil {
			// Retain only recognized values, not the entire environment buffer.
			*target = strings.Clone(strings.TrimSpace(value))
			// The first occurrence wins, including an explicitly empty value.
			delete(remaining, key)
		}
	}
	return hints
}

func splitLinuxArgs(data []byte) []string {
	raw := strings.Split(string(data), "\x00")
	args := make([]string, 0, len(raw))
	for _, arg := range raw {
		if arg != "" {
			args = append(args, arg)
		}
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

//nolint:cyclop // proc stat validation checks each required field independently
func parseLinuxStat(stat string) (Process, error) {
	openParen := strings.IndexByte(stat, '(')
	closeParen := strings.LastIndexByte(stat, ')')
	if openParen <= 0 || closeParen < openParen || closeParen+2 >= len(stat) {
		return Process{}, errMissingCommandDelimiter
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stat[:openParen]))
	if err != nil || pid <= 0 {
		return Process{}, errInvalidProcessID
	}
	fields := strings.Fields(stat[closeParen+2:])
	const (
		stateIndex  = 0
		ppidIndex   = 1
		pgrpIndex   = 2
		ttpgidIndex = 5
		startIndex  = 19
	)
	if len(fields) <= startIndex {
		return Process{}, errTruncatedStatRecord
	}
	if len(fields[stateIndex]) != 1 {
		return Process{}, errInvalidProcessState
	}
	ppid, err := strconv.Atoi(fields[ppidIndex])
	if err != nil || ppid < 0 {
		return Process{}, errInvalidParentProcessID
	}
	pgrp, err := strconv.Atoi(fields[pgrpIndex])
	if err != nil || pgrp < 0 {
		return Process{}, errInvalidProcessGroupID
	}
	ttpgid, err := strconv.Atoi(fields[ttpgidIndex])
	if err != nil || ttpgid < -1 {
		return Process{}, errInvalidForegroundGroup
	}
	if _, err := strconv.ParseUint(fields[startIndex], 10, 64); err != nil {
		return Process{}, errInvalidProcessStartTime
	}
	return Process{
		PID: pid, PPID: ppid, ProcessGroupID: pgrp, Foreground: ttpgid > 0 && pgrp == ttpgid,
		StartIdentity: fields[startIndex], Executable: "", CWD: "", TTY: "", AgentHint: "",
		MultiplexerKind: "", MultiplexerServer: "", MultiplexerSession: "", MultiplexerPane: "", Args: nil,
	}, nil
}

func classifyProcessError(path string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrPermission) {
		return &PermissionError{Path: path, Err: err}
	}
	return &TableError{Path: path, Err: err}
}

func linuxStartTimeFromStat(stat string) string {
	closeParen := strings.LastIndexByte(stat, ')')
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return ""
	}
	fields := strings.Fields(stat[closeParen+2:])
	const startTimeFieldIndexAfterComm = 19
	if len(fields) <= startTimeFieldIndexAfterComm {
		return ""
	}
	if _, err := strconv.ParseUint(fields[startTimeFieldIndexAfterComm], 10, 64); err != nil {
		return ""
	}
	return fields[startTimeFieldIndexAfterComm]
}
