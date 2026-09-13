package cli

import (
	"context"
	"fmt"
	"io"
)

func runTestCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runTestCLIWithStdin(ctx, args, nil, stdout, stderr)
}

func runTestCLIWithStdin(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	app := &application{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	cmd := app.newRootCommand()
	if err := cmd.Run(ctx, append([]string{"aht"}, args...)); err != nil {
		return fmt.Errorf("run test cli: %w", err)
	}
	return nil
}
