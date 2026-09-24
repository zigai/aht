package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func executeSurfaceCommand(t *testing.T, stdout *bytes.Buffer, args ...string) {
	t.Helper()
	stdout.Reset()
	if err := runTestCLI(context.Background(), args, stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func requireSurfaceOutput(t *testing.T, output string, message string, fragments ...string) {
	t.Helper()
	output = strings.Join(strings.Fields(output), " ")
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("%s: %q", message, output)
		}
	}
}

func observeTestSession(t *testing.T, store registry.Store, sessionID string, at time.Time) registry.Session {
	t.Helper()
	activity := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: at, Subject: registry.ObservationIdentity{SessionID: sessionID}, Evidence: &registry.Report{Activity: &activity}})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

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
