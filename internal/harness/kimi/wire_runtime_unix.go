//go:build linux || darwin

package kimi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

const fatalSignalOffset = 128

func duplicateStream(source *os.File) (*os.File, func(), error) {
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, nil, fmt.Errorf("getting raw stream connection: %w", err)
	}
	var fd uintptr
	var flags, duplicate int
	var descriptorErr error
	if err := raw.Control(func(value uintptr) {
		fd = value
		flags, descriptorErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		if descriptorErr == nil {
			duplicate, descriptorErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
		}
	}); err != nil {
		return nil, nil, fmt.Errorf("controlling stream descriptor: %w", err)
	}
	if descriptorErr != nil {
		return nil, nil, fmt.Errorf("duplicating stream descriptor: %w", descriptorErr)
	}
	if err := unix.SetNonblock(duplicate, true); err != nil {
		_ = unix.Close(duplicate)
		return nil, nil, fmt.Errorf("setting non-blocking stream: %w", err)
	}
	file := os.NewFile(uintptr(duplicate), "kimi-wire-stream")
	// dup shares file status flags. Restore them only after owned I/O has
	// stopped; the caller retains ownership of the original descriptor.
	restore := func() {
		_ = file.Close()
		_, _ = unix.FcntlInt(fd, unix.F_SETFL, flags)
	}
	return file, restore, nil
}

func ownProcessGroup(command *exec.Cmd) error {
	//nolint:exhaustruct_v5 // only Setpgid is required for process-group isolation
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

func killProcessGroup(command *exec.Cmd) {
	// This group was created for this exact child, never discovered by scans.
	// ESRCH is expected after normal exit; all other exits still reap the child.
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func nativeExitCode(exit *exec.ExitError) int {
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return fatalSignalOffset + int(status.Signal())
	}
	return exit.ExitCode()
}

func isBrokenPipe(err error) bool { return errors.Is(err, syscall.EPIPE) }
