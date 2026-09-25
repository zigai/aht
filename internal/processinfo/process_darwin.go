//go:build darwin

package processinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/zigai/aht/v2/internal/command"
)

const darwinPSMinimumFieldCount = 6

var (
	errNilDarwinProcessTable        = errors.New("nil process table")
	errInvalidDarwinProcessRecord   = errors.New("invalid process record")
	errTruncatedDarwinPSRecord      = errors.New("truncated ps record")
	errInvalidDarwinPSPID           = errors.New("invalid ps pid")
	errInvalidDarwinPSParentPID     = errors.New("invalid ps parent pid")
	errInvalidDarwinPSProcessGroup  = errors.New("invalid ps process group id")
	errInvalidDarwinPSForegroundPID = errors.New("invalid ps foreground process group id")
	errInvalidDarwinLsofPID         = errors.New("invalid lsof pid")
)

type darwinPSRow struct {
	PID            int
	PPID           int
	ProcessGroupID int
	Foreground     bool
	TTY            string
	Executable     string
	Args           []string
}

// List returns a current-user process snapshot from the kernel process table,
// enriched by one ps and one lsof invocation.
func List(ctx context.Context) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	kinfo, err := unix.SysctlKinfoProcSlice("kern.proc.uid", unix.Getuid())
	if err != nil {
		return nil, classifyDarwinError("kern.proc.uid", err)
	}
	processes, pids, err := darwinKernelProcesses(kinfo)
	if err != nil {
		return nil, err
	}
	if len(pids) == 0 {
		return processes, nil
	}

	byPID, err := darwinPSInventory(ctx, pids)
	if err != nil {
		return nil, err
	}
	enrichDarwinProcesses(processes, byPID)
	if cwd, err := darwinLsofCWD(ctx, pids); err == nil {
		for i := range processes {
			processes[i].CWD = cwd[processes[i].PID]
		}
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("inspect process working directories: %w", ctxErr)
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
	processes, err := List(ctx)
	if err != nil {
		return zero, false, err
	}
	for _, process := range processes {
		if process.PID == pid {
			return process, true, nil
		}
	}
	return zero, false, nil
}

// StartIdentity returns the kernel process-start identity used by List and
// Find, so callers can safely compare identities across both lookup paths.
func StartIdentity(ctx context.Context, pid int) string {
	if pid <= 0 || ctx.Err() != nil {
		return ""
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || process == nil || int(process.Proc.P_pid) != pid {
		return ""
	}
	identity, ok := darwinProcessStartIdentity(*process)
	if !ok {
		return ""
	}

	return identity
}

// CommandName returns the executable command name for pid on macOS.
func CommandName(ctx context.Context, pid int) (string, error) {
	if pid <= 0 {
		return "", nil
	}
	output, err := command.Run(ctx, "/bin/ps", nil, "-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("checking context: %w", ctxErr)
		}
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
}

func darwinPSInventory(ctx context.Context, pids []string) (map[int]darwinPSRow, error) {
	output, err := command.Run(ctx, "/bin/ps", nil, "-o", "pid=,ppid=,pgid=,tpgid=,tty=,comm=,args=", "-p", strings.Join(pids, ","))
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
		if row.Executable != "" {
			processes[i].Executable = row.Executable
		}
		processes[i].TTY = row.TTY
		processes[i].Args = row.Args
	}
}

func darwinKernelProcesses(kinfo []unix.KinfoProc) ([]Process, []string, error) {
	if kinfo == nil {
		return nil, nil, &TableError{Path: "kern.proc.uid", Err: errNilDarwinProcessTable}
	}
	pids := make([]string, 0, len(kinfo))
	processes := make([]Process, 0, len(kinfo))
	for _, process := range kinfo {
		pid := int(process.Proc.P_pid)
		startIdentity, validStart := darwinProcessStartIdentity(process)
		if pid <= 0 || process.Eproc.Ppid < 0 || process.Eproc.Pgid < 0 || !validStart {
			return nil, nil, &TableError{Path: "kern.proc.uid", Err: fmt.Errorf("pid %d: %w", pid, errInvalidDarwinProcessRecord)}
		}
		pids = append(pids, strconv.Itoa(pid))
		processes = append(processes, Process{
			PID:                pid,
			PPID:               int(process.Eproc.Ppid),
			ProcessGroupID:     int(process.Eproc.Pgid),
			Foreground:         false,
			StartIdentity:      startIdentity,
			Executable:         darwinCString(process.Proc.P_comm[:]),
			CWD:                "",
			TTY:                "",
			AgentHint:          "",
			MultiplexerKind:    "",
			MultiplexerServer:  "",
			MultiplexerSession: "",
			MultiplexerPane:    "",
			Args:               nil,
		})
	}
	return processes, pids, nil
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
	if len(fields) < darwinPSMinimumFieldCount {
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
		TTY:            fields[4],
		Executable:     fields[5],
		Args:           append([]string(nil), fields[darwinPSMinimumFieldCount:]...),
	}, nil
}

func darwinLsofCWD(ctx context.Context, pids []string) (map[int]string, error) {
	output, err := command.Run(ctx, "/usr/sbin/lsof", nil, "-a", "-d", "cwd", "-p", strings.Join(pids, ","), "-Fn")
	if err != nil {
		return nil, fmt.Errorf("run lsof process inventory: %w", err)
	}
	cwds := make(map[int]string)
	pid := 0
	for line := range strings.SplitSeq(string(output), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			value, err := strconv.Atoi(line[1:])
			if err != nil || value <= 0 {
				return nil, errInvalidDarwinLsofPID
			}
			pid = value
		case 'n':
			if pid > 0 {
				cwds[pid] = line[1:]
			}
		}
	}
	return cwds, nil
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
