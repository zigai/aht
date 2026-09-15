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
	//nolint:contextcheck // Command tree is built before ExecuteContext receives the context.
	cmd := app.newRootCommand()
	cmd.SetArgs(args)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("run test cli: %w", err)
	}
	return nil
}
