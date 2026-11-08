//go:build linux

package processinfo

import (
	"bytes"
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

type linuxProcessReader struct {
	environment bytes.Buffer
}

// List returns a complete best-effort snapshot of processes owned by the
// effective user. Processes which exit while being read are skipped.
func List(ctx context.Context) ([]Process, error) {
	return ListWithEnv(ctx, nil, nil)
}

// ListWithEnv is List that also records the values of envKeys in Process.Env.
func ListWithEnv(
	ctx context.Context,
	envKeys []string,
	includeEnv func(Process) bool,
) ([]Process, error) {
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

	return listLinuxProcessEntries(ctx, entries, boot, uid, envKeys, includeEnv)
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
		if isLinuxNotExist(err) {
			return zero, false, nil
		}

		return zero, false, classifyProcessError(dir, err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return zero, false, nil
	}

	var reader linuxProcessReader

	process, err := reader.read(dir, pid, boot, nil, nil)
	if err != nil {
		if isLinuxProcessExited(dir, pid, err) {
			return zero, false, nil
		}

		return zero, false, err
	}

	return process, true, nil
}

// StartIdentity returns a boot-qualified process start identity for pid.
func StartIdentity(ctx context.Context, pid int) string {
	process, found, err := Find(ctx, pid)
	if err != nil || !found {
		return ""
	}

	return process.StartIdentity
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
	path := filepath.Join(procRoot, "sys", "kernel", "random", "boot_id")

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

func (r *linuxProcessReader) read(
	dir string,
	pid int,
	boot string,
	envKeys []string,
	includeEnv func(Process) bool,
) (Process, error) {
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

	if len(envKeys) > 0 && (includeEnv == nil || includeEnv(info)) {
		info.Env = r.readEnvironment(filepath.Join(dir, "environ"), envKeys)
	}

	return info, nil
}

func (r *linuxProcessReader) readEnvironment(path string, keys []string) map[string]string {
	r.environment.Reset()

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	if _, err := r.environment.ReadFrom(file); err != nil {
		return nil
	}

	return parseEnvironment(r.environment.Bytes(), keys)
}

func readLinuxLink(path string) string {
	link, err := os.Readlink(path)
	if err != nil {
		return ""
	}

	return link
}

func isLinuxTTY(path string) bool {
	return path == "/dev/tty" || strings.HasPrefix(path, "/dev/pts/") ||
		strings.HasPrefix(path, "/dev/tty")
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

func listLinuxProcessEntries(
	ctx context.Context,
	entries []os.DirEntry,
	boot string,
	uid uint32,
	envKeys []string,
	includeEnv func(Process) bool,
) ([]Process, error) {
	processes := make([]Process, 0, len(entries))

	var reader linuxProcessReader

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("process listing context: %w", err)
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}

		dir := filepath.Join(procRoot, entry.Name())

		owned, err := linuxProcessOwned(dir, uid)
		if err != nil {
			return nil, err
		}

		if !owned {
			continue
		}

		process, err := reader.read(dir, pid, boot, envKeys, includeEnv)
		if err != nil {
			if isLinuxProcessExited(dir, pid, err) {
				continue
			}

			return nil, err
		}

		processes = append(processes, process)
	}

	return processes, nil
}

func linuxProcessOwned(dir string, uid uint32) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if isLinuxNotExist(err) {
			return false, nil
		}

		return false, classifyProcessError(dir, err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)

	return ok && stat.Uid == uid, nil
}

func parseLinuxStat(stat string) (Process, error) {
	pid, name, tail, err := parseLinuxStatHeader(stat)
	if err != nil {
		return Process{}, err
	}

	fields := strings.Fields(tail)

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
		PID:            pid,
		Name:           name,
		PPID:           ppid,
		ProcessGroupID: pgrp,
		Foreground:     ttpgid > 0 && pgrp == ttpgid,
		StartIdentity:  fields[startIndex],
		Executable:     "",
		CWD:            "",
		TTY:            "",
		Env:            nil,
		Args:           nil,
	}, nil
}

func parseLinuxStatHeader(stat string) (int, string, string, error) {
	openParen := strings.IndexByte(stat, '(')
	closeParen := strings.LastIndexByte(stat, ')')

	if openParen <= 0 || closeParen < openParen || closeParen+2 >= len(stat) {
		return 0, "", "", errMissingCommandDelimiter
	}

	pid, err := strconv.Atoi(strings.TrimSpace(stat[:openParen]))
	if err != nil || pid <= 0 {
		return 0, "", "", errInvalidProcessID
	}

	return pid, stat[openParen+1 : closeParen], stat[closeParen+2:], nil
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

func isLinuxNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func isLinuxProcessExited(dir string, pid int, err error) bool {
	if isLinuxNotExist(err) {
		return true
	}

	if _, statErr := os.Stat(dir); statErr != nil && isLinuxNotExist(statErr) {
		return true
	}

	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
