package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func (app *application) confirmAction(
	ctx context.Context,
	in io.Reader,
	prompt string,
	correctiveFlag string,
) (bool, error) {
	if in == nil {
		in = app.stdin
	}

	if in == nil {
		in = os.Stdin
	}

	file, isFile := in.(*os.File)
	if !isFile || !term.IsTerminal(int(file.Fd())) {
		return false, exitCode(
			fmt.Errorf(
				"%w; use %s to confirm non-interactively",
				errTerminalRequired,
				correctiveFlag,
			),
			exitCodeUsage,
		)
	}

	out := app.stderr
	if out == nil {
		out = os.Stderr
	}

	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, fmt.Errorf("writing confirmation prompt: %w", err)
	}

	return readConfirmationLine(ctx, file)
}

func readConfirmationLine(ctx context.Context, file *os.File) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, exitCode(err, exitCodeInterrupted)
	}

	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		return false, fmt.Errorf("pipe for confirmation: %w", err)
	}

	defer func() {
		_ = rPipe.Close()
		_ = wPipe.Close()
	}()

	stop := context.AfterFunc(ctx, func() {
		_, _ = wPipe.Write([]byte{1})
	})
	defer stop()

	pollFds := []unix.PollFd{
		{
			Fd:     int32(file.Fd()), //nolint:gosec // terminal file descriptor fits in int32
			Events: unix.POLLIN,
		},
		{
			Fd:     int32(rPipe.Fd()), //nolint:gosec // pipe file descriptor fits in int32
			Events: unix.POLLIN,
		},
	}
	for {
		_, err := unix.Poll(pollFds, -1)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}

			return false, fmt.Errorf("polling confirmation: %w", err)
		}

		break
	}

	if pollFds[1].Revents&unix.POLLIN != 0 || ctx.Err() != nil {
		return false, exitCode(context.Canceled, exitCodeInterrupted)
	}

	reader := bufio.NewReader(file)

	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}

		return false, fmt.Errorf("reading confirmation: %w", err)
	}

	ans := strings.ToLower(strings.TrimSpace(line))

	return ans == "y" || ans == "yes", nil
}
