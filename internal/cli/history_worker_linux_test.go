package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type historyPriorityObservation struct {
	Nice    int `json:"nice"`
	IOClass int `json:"io_class"`
}

type historyPriorityResult struct {
	Priorities []historyPriorityObservation `json:"priorities"`
	Arguments  []string                     `json:"arguments"`
}

func TestHistoryRefreshCommandInheritsPrioritiesAndArguments(t *testing.T) {
	const childMarker = "history-priority-child"
	if marker := slices.Index(os.Args, childMarker); marker >= 0 {
		result := collectHistoryPriorities(os.Args[marker+1:])
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			panic(err)
		}
		os.Exit(0)
	}
	t.Setenv("PATH", t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"argument with spaces", "semi;colon", "--literal"}
	args := append([]string{"-test.run=^TestHistoryRefreshCommandInheritsPrioritiesAndArguments$", "--", childMarker}, arguments...)
	var stdout, stderr bytes.Buffer
	before := historyProcessPriority(t)
	if err := runHistoryCommand(t.Context(), executable, args, &stdout, &stderr); err != nil {
		t.Fatalf("refresh process: %v, stderr=%s", err, &stderr)
	}
	var result historyPriorityResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("priority output=%s: %v", &stdout, err)
	}
	if !slices.Equal(result.Arguments, arguments) || len(result.Priorities) != 4 {
		t.Fatalf("child result=%+v", result)
	}
	for _, observed := range result.Priorities {
		if observed.Nice != 19 || observed.IOClass != 3 {
			t.Fatalf("child thread priority=%+v, want nice=19 io=3", observed)
		}
	}
	after := historyProcessPriority(t)
	if before != after {
		t.Fatalf("tracker priority changed: before=%+v after=%+v", before, after)
	}
}

func historyProcessPriority(t *testing.T) historyPriorityObservation {
	t.Helper()
	priority, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	priorityIO, _, errno := unix.Syscall(unix.SYS_IOPRIO_GET, 1, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	return historyPriorityObservation{Nice: 20 - priority, IOClass: int(priorityIO >> 13)}
}

func collectHistoryPriorities(arguments []string) historyPriorityResult {
	observations := make(chan historyPriorityObservation)
	release := make(chan struct{})
	var threads sync.WaitGroup
	for range 4 {
		threads.Go(func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			priority, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
			if err != nil {
				panic(err)
			}
			priorityIO, _, errno := unix.Syscall(unix.SYS_IOPRIO_GET, 1, 0, 0)
			if errno != 0 {
				panic(errno)
			}
			observations <- historyPriorityObservation{Nice: 20 - priority, IOClass: int(priorityIO >> 13)}
			<-release
		})
	}
	result := historyPriorityResult{Arguments: arguments, Priorities: nil}
	for range 4 {
		result.Priorities = append(result.Priorities, <-observations)
	}
	close(release)
	threads.Wait()
	return result
}

type detachedHistoryObservation struct {
	PID        int                          `json:"pid"`
	SID        int                          `json:"sid"`
	PGID       int                          `json:"pgid"`
	Files      [3]string                    `json:"files"`
	NoTerminal bool                         `json:"no_terminal"`
	Priorities []historyPriorityObservation `json:"priorities"`
}

type detachedHistoryTestProcess struct {
	connection *net.UnixConn
	pid        int
	reaped     bool
	started    chan struct{}
	launchErr  error
	deadline   time.Time
}

func TestDetachedHistoryCommandReturnsWhileChildRunsWithoutTerminal(t *testing.T) {
	const childMarker = "history-detached-child"
	if marker := slices.Index(os.Args, childMarker); marker >= 0 {
		observeDetachedHistoryChild(t, os.Args[marker+1])
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name            string
		suppliedStreams bool
	}{
		{name: "default streams", suppliedStreams: false},
		{name: "supplied streams", suppliedStreams: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := historyProcessPriority(t)
			child := newDetachedHistoryTestProcess(t, executable, childMarker, tt.suppliedStreams)
			var observed detachedHistoryObservation
			if err := json.NewDecoder(child.connection).Decode(&observed); err != nil {
				t.Fatalf("child observation: %v", err)
			}
			child.awaitLaunch(t)
			if after := historyProcessPriority(t); before != after {
				t.Errorf("caller priority changed: before=%+v after=%+v", before, after)
			}
			assertDetachedHistoryObservation(t, child.pid, observed)
			child.assertRunning(t)
			child.finish(t)
		})
	}
}

func newDetachedHistoryTestProcess(t *testing.T, executable, childMarker string, suppliedStreams bool) *detachedHistoryTestProcess {
	t.Helper()
	address := &net.UnixAddr{Name: "@aht-history-detached-" + strconv.Itoa(os.Getpid()), Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	child := &detachedHistoryTestProcess{started: make(chan struct{}), deadline: time.Now().Add(10 * time.Second)}
	if err := listener.SetDeadline(child.deadline); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), executable, "-test.run=^TestDetachedHistoryCommandReturnsWhileChildRunsWithoutTerminal$", "--", childMarker, address.Name)
	var stdout, stderr bytes.Buffer
	if suppliedStreams {
		command.Stdin = strings.NewReader("caller input")
		command.Stdout, command.Stderr = &stdout, &stderr
	}
	t.Cleanup(func() { child.cleanup(t) })
	go func() {
		child.launchErr = startDetachedHistoryCommand(command)
		if child.launchErr != nil {
			_ = listener.Close()
		}
		close(child.started)
	}()
	child.connection, err = listener.AcceptUnix()
	if err != nil {
		t.Fatalf("child connection: %v", err)
	}
	child.pid = detachedHistoryTestPeerPID(t, child.connection)
	if err := child.connection.SetDeadline(child.deadline); err != nil {
		t.Fatal(err)
	}
	return child
}

func detachedHistoryTestPeerPID(t *testing.T, connection *net.UnixConn) int {
	t.Helper()
	raw, err := connection.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var credentials *unix.Ucred
	var credentialsErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, credentialsErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		t.Fatal(err)
	}
	if credentialsErr != nil {
		t.Fatal(credentialsErr)
	}
	return int(credentials.Pid)
}

func (child *detachedHistoryTestProcess) cleanup(t *testing.T) {
	t.Helper()
	if child.connection != nil {
		_ = child.connection.Close()
	}
	if child.pid > 0 && !child.reaped {
		if err := unix.Kill(child.pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			t.Errorf("kill detached child %d: %v", child.pid, err)
		}
		if _, err := waitHistoryTestChild(child.pid); err != nil && !errors.Is(err, unix.ECHILD) {
			t.Errorf("reap detached child %d: %v", child.pid, err)
		}
	}
	select {
	case <-child.started:
	case <-time.After(10 * time.Second):
		t.Error("detached launcher did not finish after child cleanup")
	}
}

func (child *detachedHistoryTestProcess) awaitLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-child.started:
		if child.launchErr != nil {
			t.Fatalf("detached launch: %v", child.launchErr)
		}
	case <-time.After(time.Until(child.deadline)):
		t.Fatal("detached launcher waited for unfinished child work")
	}
}

func assertDetachedHistoryObservation(t *testing.T, pid int, observed detachedHistoryObservation) {
	t.Helper()
	if observed.PID != pid || observed.SID != pid || observed.PGID != pid || !observed.NoTerminal {
		t.Errorf("child identity/terminal=%+v, want PID=SID=PGID=%d and no controlling terminal", observed, pid)
	}
	if observed.Files != [3]string{"/dev/null", "/dev/null", "/dev/null"} {
		t.Errorf("child descriptors=%q, want /dev/null for stdin/stdout/stderr", observed.Files)
	}
	if len(observed.Priorities) != 4 {
		t.Fatalf("child priorities=%+v, want four runtime threads", observed.Priorities)
	}
	for _, priority := range observed.Priorities {
		if priority.Nice != 19 || priority.IOClass != 3 {
			t.Errorf("detached child thread priority=%+v, want nice=19 io=3", priority)
		}
	}
}

func (child *detachedHistoryTestProcess) assertRunning(t *testing.T) {
	t.Helper()
	if waited, err := unix.Wait4(child.pid, nil, unix.WNOHANG, nil); err != nil || waited != 0 {
		child.reaped = waited == child.pid || errors.Is(err, unix.ECHILD)
		t.Fatalf("child did not outlive launch: waited=%d err=%v", waited, err)
	}
}

func (child *detachedHistoryTestProcess) finish(t *testing.T) {
	t.Helper()
	if _, err := child.connection.Write([]byte{'x'}); err != nil {
		t.Fatalf("release child: %v", err)
	}
	if output, err := io.ReadAll(child.connection); err != nil || len(output) != 0 {
		t.Fatalf("child completion output=%q err=%v", output, err)
	}
	status, err := waitHistoryTestChild(child.pid)
	child.reaped = err == nil || errors.Is(err, unix.ECHILD)
	if err != nil || !status.Exited() || status.ExitStatus() != 0 {
		t.Fatalf("detached child exit=%v err=%v", status, err)
	}
}

func observeDetachedHistoryChild(t *testing.T, address string) {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	observed := detachedHistoryObservation{PID: os.Getpid()}
	observed.SID, err = unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	observed.PGID, err = unix.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	for fd := range observed.Files {
		observed.Files[fd], err = os.Readlink("/proc/self/fd/" + strconv.Itoa(fd))
		if err != nil {
			t.Fatal(err)
		}
	}
	terminal, err := unix.Open("/dev/tty", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	observed.NoTerminal = errors.Is(err, unix.ENXIO)
	if err == nil {
		if err := unix.Close(terminal); err != nil {
			t.Fatal(err)
		}
	}
	observed.Priorities = collectHistoryPriorities(nil).Priorities
	if err := json.NewEncoder(connection).Encode(observed); err != nil {
		t.Fatal(err)
	}
	var release [1]byte
	if _, err := io.ReadFull(connection, release[:]); err != nil {
		t.Fatal(err)
	}
	if release[0] != 'x' {
		t.Fatalf("release byte=%q", release)
	}
}

func waitHistoryTestChild(pid int) (unix.WaitStatus, error) {
	var status unix.WaitStatus
	for {
		_, err := unix.Wait4(pid, &status, 0, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return status, fmt.Errorf("wait for detached history child %d: %w", pid, err)
		}
		return status, nil
	}
}

func TestDetachedHistoryCommandReturnsLaunchFailure(t *testing.T) {
	command := exec.CommandContext(context.Background(), filepath.Join(t.TempDir(), "missing-history-executable"))
	if err := startDetachedHistoryCommand(command); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch error=%v, want missing executable", err)
	}
}

func TestHistoryRefreshCommandCancellationKillsAndReapsChild(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	done := make(chan error, 1)
	var stderr bytes.Buffer
	go func() {
		err := runHistoryCommand(ctx, "/bin/sh", []string{"-c", "printf 'ready\\n'; exec sleep 3600"}, writer, &stderr)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	if line, err := bufio.NewReader(reader).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child readiness=%q err=%v", line, err)
	}
	cancel()
	err := <-done
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ProcessState.Success() {
		t.Fatalf("canceled child error=%v", err)
	}
	if _, err := unix.Wait4(exit.ProcessState.Pid(), nil, unix.WNOHANG, nil); !errors.Is(err, unix.ECHILD) {
		t.Fatalf("refresh process was not reaped: %v", err)
	}
}
