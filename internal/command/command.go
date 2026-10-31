// Package command runs local inspection commands with bounded resources.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	defaultTimeout     = 5 * time.Second
	defaultOutputLimit = 4 << 20
	pipeWaitDelay      = time.Second
)

var (
	ErrOutputLimit   = errors.New("command output exceeds limit")
	errInvalidLimits = errors.New("command timeout and output limit must be positive")
)

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelCauseFunc
}

// Run returns stdout on success and stdout plus stderr on failure. Each stream
// is limited to 4 MiB and execution to five seconds. A nil env inherits the
// process environment. Earlier caller cancellation/deadlines remain effective.
func Run(ctx context.Context, executable string, env []string, args ...string) ([]byte, error) {
	return RunWithLimits(ctx, defaultTimeout, defaultOutputLimit, executable, env, args...)
}

// RunWithLimits is Run with operation-specific timeout and per-stream byte limits.
// Oversized output fails rather than returning a successful truncated response.
func RunWithLimits(
	parent context.Context,
	timeout time.Duration,
	outputLimit int,
	executable string,
	env []string,
	args ...string,
) ([]byte, error) {
	if timeout <= 0 || outputLimit <= 0 {
		return nil, errInvalidLimits
	}

	timedCtx, stopTimeout := context.WithTimeout(parent, timeout)
	defer stopTimeout()

	ctx, cancel := context.WithCancelCause(timedCtx)
	defer cancel(nil)

	stdout := limitedBuffer{buffer: bytes.Buffer{}, limit: outputLimit, cancel: cancel}
	stderr := limitedBuffer{buffer: bytes.Buffer{}, limit: outputLimit, cancel: cancel}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = pipeWaitDelay

	err := cmd.Run()
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}

	if err != nil {
		output := append(bytes.Clone(stdout.buffer.Bytes()), stderr.buffer.Bytes()...)
		return output, fmt.Errorf("run %s: %w", executable, err)
	}

	return stdout.buffer.Bytes(), nil
}

func (writer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := writer.limit - writer.buffer.Len()
	if len(data) > remaining {
		_, _ = writer.buffer.Write(data[:remaining])
		writer.cancel(ErrOutputLimit)

		return remaining, ErrOutputLimit
	}

	written, _ := writer.buffer.Write(data) // bytes.Buffer.Write always returns a nil error.

	return written, nil
}
