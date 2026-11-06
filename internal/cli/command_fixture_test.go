package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

func observeTestSession(
	t *testing.T,
	store registry.Store,
	sessionID string,
	at time.Time,
) registry.Session {
	t.Helper()

	activity := registry.ActivityIdle

	session, err := store.Observe(
		context.Background(),
		registry.Observation{
			Harness:  registry.Harness("codex"),
			At:       at,
			Subject:  registry.ObservationIdentity{SessionID: sessionID},
			Evidence: &registry.Report{Activity: &activity},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	return session
}

func runTestCLI(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	return runTestCLIWithStdin(ctx, args, nil, stdout, stderr)
}

func runTestCLIWithStdin(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer, stderr io.Writer,
) error {
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

func listenFakeBroker(t *testing.T) net.Listener {
	t.Helper()

	//nolint:usetesting // reason: t.TempDir paths can exceed the Unix socket path limit.
	socketFile, err := os.CreateTemp("", "aht-broker-")
	if err != nil {
		t.Fatal(err)
	}

	socketPath := socketFile.Name()
	_ = socketFile.Close()

	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}

	t.Setenv(broker.SocketPathEnv, socketPath)

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	return listener
}
