//go:build darwin

package processinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/zigai/aht/v2/internal/command"
)

const (
	darwinPSFieldCount = 5
	darwinPIDSysctl    = "kern.proc.pid"
	darwinUIDSysctl    = "kern.proc.uid"
)

var (
	errNilDarwinProcessTable        = errors.New("nil process table")
	errInvalidDarwinProcessRecord   = errors.New("invalid process record")
	errTruncatedDarwinPSRecord      = errors.New("truncated ps record")
	errInvalidDarwinPSPID           = errors.New("invalid ps pid")
	errInvalidDarwinPSParentPID     = errors.New("invalid ps parent pid")
	errInvalidDarwinPSProcessGroup  = errors.New("invalid ps process group id")
	errInvalidDarwinPSForegroundPID = errors.New("invalid ps foreground process group id")
)

type darwinPSRow struct {
	PID            int
	PPID           int
	ProcessGroupID int
	Foreground     bool
	TTY            string
}

// List returns a current-user process snapshot from the kernel process table,
// enriched by kern.procargs2, one ps, and one lsof invocation.
func List(ctx context.Context) ([]Process, error) {
	return ListWithEnv(ctx, nil, nil)
}

// ListWithEnv also reads requested environment keys when kern.procargs2 exposes them.
func ListWithEnv(
	ctx context.Context,
	envKeys []string,
	includeEnv func(Process) bool,
) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}

	kinfo, err := unix.SysctlKinfoProcSlice(darwinUIDSysctl, unix.Getuid())
	if err != nil {
		return nil, classifyDarwinError(darwinUIDSysctl, err)
	}

	processes, pids, err := darwinKernelProcesses(kinfo)
	if err != nil {
		return nil, err
	}

	if len(pids) == 0 {
		return processes, nil
	}

	if err := enrichDarwinInventory(ctx, processes, pids, envKeys, includeEnv); err != nil {
		return nil, err
	}

	return processes, nil
}

// Find returns the current-user process identified by pid. A false found
// result means the process does not exist or belongs to another user.
func Find(ctx context.Context, pid int) (Process, bool, error) {
	var zero Process
	if pid <= 0 {
		return zero, false, nil
	}

	if err := ctx.Err(); err != nil {
		return zero, false, fmt.Errorf("find process: %w", err)
	}

	kinfo, err := unix.SysctlKinfoProc(darwinPIDSysctl, pid)
	// SysctlKinfoProc returns EIO when the kernel returns no record.
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EIO) {
		return zero, false, nil
	}

	if err != nil {
		return zero, false, classifyDarwinError(darwinPIDSysctl, err)
	}

	if kinfo == nil || int(kinfo.Proc.P_pid) != pid || int(kinfo.Eproc.Ucred.Uid) != unix.Getuid() {
		return zero, false, nil
	}

	process, err := darwinKernelProcess(*kinfo, darwinPIDSysctl)
	if err != nil {
		return zero, false, err
	}

	processes := []Process{process}
	if err := enrichDarwinInventory(
		ctx,
		processes,
		[]string{strconv.Itoa(pid)},
		nil,
		nil,
	); err != nil {
		return zero, false, err
	}

	return processes[0], true, nil
}

// StartIdentity returns the kernel process-start identity used by List and
// Find, so callers can safely compare identities across both lookup paths.
func StartIdentity(ctx context.Context, pid int) string {
	if pid <= 0 || ctx.Err() != nil {
		return ""
	}

	process, err := unix.SysctlKinfoProc(darwinPIDSysctl, pid)
	if err != nil || process == nil || int(process.Proc.P_pid) != pid {
		return ""
	}

	identity, ok := darwinProcessStartIdentity(*process)
	if !ok {
		return ""
	}

	return identity
}

func CommandName(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", nil
	}

	output, err := command.Run(ctx, "/bin/ps", nil, "-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("checking context: %w", ctxErr)
		}

		return "", classifyDarwinError("/bin/ps", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func enrichDarwinInventory(
	ctx context.Context,
	processes []Process,
	pids []string,
	envKeys []string,
	includeEnv func(Process) bool,
) error {
	byPID, err := darwinPSInventory(ctx, pids)
	if err != nil {
		return err
	}

	enrichDarwinProcesses(processes, byPID)

	if cwd, err := darwinLsofCWD(ctx, pids); err == nil {
		for i := range processes {
			processes[i].CWD = cwd[processes[i].PID]
		}
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("inspect process working directories: %w", ctxErr)
	}

	return readDarwinArguments(ctx, processes, envKeys, includeEnv)
}

func darwinPSInventory(ctx context.Context, pids []string) (map[int]darwinPSRow, error) {
	output, err := command.Run(
		ctx,
		"/bin/ps",
		nil,
		"-o",
		"pid=,ppid=,pgid=,tpgid=,tty=",
		"-p",
		strings.Join(pids, ","),
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("wait for ps process inventory: %w", ctxErr)
		}

		return nil, classifyDarwinError("/bin/ps", err)
	}

	rows, err := parseDarwinPS(string(output))
	if err != nil {
		return nil, &TableError{Path: "/bin/ps", Err: err}
	}

	return rows, nil
}

func enrichDarwinProcesses(processes []Process, rows map[int]darwinPSRow) {
	for i := range processes {
		row, ok := rows[processes[i].PID]
		if !ok {
			continue
		}

		processes[i].PPID = row.PPID
		processes[i].ProcessGroupID = row.ProcessGroupID

		processes[i].Foreground = row.Foreground
		processes[i].TTY = row.TTY
	}
}

func readDarwinArguments(
	ctx context.Context,
	processes []Process,
	envKeys []string,
	includeEnv func(Process) bool,
) error {
	for i := range processes {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read process arguments: %w", err)
		}

		data, err := unix.SysctlRaw("kern.procargs2", processes[i].PID)
		if darwinArgumentsUnavailable(err) {
			// The process exited or denies access; keep the kernel command name.
			continue
		}

		if err != nil {
			return classifyDarwinError("kern.procargs2", err)
		}

		executable, args, environment, err := parseDarwinProcArgs(data)
		if err != nil {
			return &TableError{Path: "kern.procargs2", Err: err}
		}

		if executable != "" {
			processes[i].Executable = executable
		}

		processes[i].Args = args
		if len(envKeys) > 0 && (includeEnv == nil || includeEnv(processes[i])) {
			processes[i].Env = parseEnvironment(environment, envKeys)
		}
	}

	return nil
}

func darwinArgumentsUnavailable(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EPERM) ||
		errors.Is(err, unix.EACCES) || errors.Is(err, unix.EINVAL)
}

func darwinKernelProcesses(kinfo []unix.KinfoProc) ([]Process, []string, error) {
	if kinfo == nil {
		return nil, nil, &TableError{Path: darwinUIDSysctl, Err: errNilDarwinProcessTable}
	}

	pids := make([]string, 0, len(kinfo))

	processes := make([]Process, 0, len(kinfo))
	for _, entry := range kinfo {
		process, err := darwinKernelProcess(entry, darwinUIDSysctl)
		if err != nil {
			return nil, nil, err
		}

		pids = append(pids, strconv.Itoa(process.PID))
		processes = append(processes, process)
	}

	return processes, pids, nil
}

func darwinKernelProcess(process unix.KinfoProc, path string) (Process, error) {
	pid := int(process.Proc.P_pid)

	startIdentity, validStart := darwinProcessStartIdentity(process)
	if pid <= 0 || process.Eproc.Ppid < 0 || process.Eproc.Pgid < 0 || !validStart {
		return Process{}, &TableError{
			Path: path,
			Err:  fmt.Errorf("pid %d: %w", pid, errInvalidDarwinProcessRecord),
		}
	}

	name := darwinCString(process.Proc.P_comm[:])

	return Process{
		PID:            pid,
		Name:           name,
		PPID:           int(process.Eproc.Ppid),
		ProcessGroupID: int(process.Eproc.Pgid),
		Foreground:     false,
		StartIdentity:  startIdentity,
		Executable:     name,
		CWD:            "",
		TTY:            "",
		Env:            nil,
		Args:           nil,
	}, nil
}

func parseDarwinPS(output string) (map[int]darwinPSRow, error) {
	rows := make(map[int]darwinPSRow)

	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		row, err := parseDarwinPSRow(strings.Fields(line))
		if err != nil {
			return nil, err
		}

		rows[row.PID] = row
	}

	return rows, nil
}

func parseDarwinPSRow(fields []string) (darwinPSRow, error) {
	if len(fields) < darwinPSFieldCount {
		return darwinPSRow{}, errTruncatedDarwinPSRecord
	}

	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return darwinPSRow{}, errInvalidDarwinPSPID
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil || ppid < 0 {
		return darwinPSRow{}, errInvalidDarwinPSParentPID
	}

	pgid, err := strconv.Atoi(fields[2])
	if err != nil || pgid < 0 {
		return darwinPSRow{}, errInvalidDarwinPSProcessGroup
	}

	tpgid, err := strconv.Atoi(fields[3])
	if err != nil || tpgid < -1 {
		return darwinPSRow{}, errInvalidDarwinPSForegroundPID
	}

	return darwinPSRow{
		PID:            pid,
		PPID:           ppid,
		ProcessGroupID: pgid,
		Foreground:     tpgid > 0 && pgid == tpgid,
		TTY:            normalizeDarwinTTY(fields[4]),
	}, nil
}

func darwinLsofCWD(ctx context.Context, pids []string) (map[int]string, error) {
	output, err := command.Run(
		ctx,
		"/usr/sbin/lsof",
		nil,
		"-a",
		"-d",
		"cwd",
		"-p",
		strings.Join(pids, ","),
		"-Fn",
	)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("run lsof process inventory: %w", ctxErr)
	}

	if err == nil {
		return parseDarwinLsofCWD(output)
	}

	if _, exited := errors.AsType[*exec.ExitError](err); exited {
		if cwds, parseErr := parseDarwinLsofCWD(output); parseErr == nil {
			return cwds, nil
		}
	}

	return nil, fmt.Errorf("run lsof process inventory: %w", err)
}

func darwinCString(data []byte) string {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		data = data[:index]
	}

	return string(data)
}

func classifyDarwinError(path string, err error) error {
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return &PermissionError{Path: path, Err: err}
	}

	return fmt.Errorf("reading process information at %s: %w", path, err)
}

func darwinProcessStartIdentity(process unix.KinfoProc) (string, bool) {
	start := process.Proc.P_starttime
	if start.Sec <= 0 || start.Usec < 0 || start.Usec >= 1_000_000 {
		return "", false
	}

	return fmt.Sprintf("%d:%06d", start.Sec, start.Usec), true
}
