package command

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const testOutputLimit = 1024

func TestCommandHelperProcess(t *testing.T) {
	mode := os.Getenv("AHT_COMMAND_TEST_MODE")
	if mode == "" {
		return
	}

	switch mode {
	case "stdout":
		_, _ = os.Stdout.WriteString("result")
		_, _ = os.Stderr.WriteString("diagnostic")
	case "overflow":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", testOutputLimit+1))
	case "stderr_overflow":
		_, _ = os.Stderr.WriteString(strings.Repeat("x", testOutputLimit+1))
	case "hang":
		time.Sleep(time.Hour)
	case "ready":
		var dialer net.Dialer

		conn, err := dialer.DialContext(t.Context(), "tcp", os.Getenv("AHT_COMMAND_TEST_ADDRESS"))
		if err != nil {
			t.Fatal(err)
		}

		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Hour)
	}

	os.Exit(0)
}

func TestRunWithLimits(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		mode    string
		want    string
		wantErr error
	}{
		{mode: "stdout", want: "result"},
		{mode: "overflow", wantErr: ErrOutputLimit},
		{mode: "stderr_overflow", wantErr: ErrOutputLimit},
		{mode: "hang", wantErr: context.DeadlineExceeded},
	} {
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()

			env := append(os.Environ(), "AHT_COMMAND_TEST_MODE="+test.mode)

			timeout := 5 * time.Second
			if test.mode == "hang" {
				timeout = 100 * time.Millisecond
			}

			output, runErr := RunWithLimits(
				t.Context(),
				timeout,
				testOutputLimit,
				executable,
				env,
				"-test.run=^TestCommandHelperProcess$",
			)
			if !errors.Is(runErr, test.wantErr) {
				t.Fatalf("error = %v, want %v", runErr, test.wantErr)
			}

			if test.wantErr == nil && string(output) != test.want {
				t.Fatalf("output = %q, want %q", output, test.want)
			}

			if len(output) > testOutputLimit {
				t.Fatalf("output length = %d, exceeds limit", len(output))
			}
		})
	}
}

func TestRunPreservesCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Run(ctx, "unneeded-command", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}

func TestRunCancelsRunningCommand(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	env := append(
		os.Environ(),
		"AHT_COMMAND_TEST_MODE=ready",
		"AHT_COMMAND_TEST_ADDRESS="+listener.Addr().String(),
	)
	done := make(chan error, 1)

	go func() {
		_, runErr := Run(ctx, executable, env, "-test.run=^TestCommandHelperProcess$")
		done <- runErr

		_ = listener.Close()
	}()

	conn, acceptErr := listener.Accept()
	if acceptErr == nil {
		_ = conn.Close()
	}

	cancel()

	runErr := <-done
	if acceptErr != nil {
		t.Fatalf("helper readiness failed: %v (command: %v)", acceptErr, runErr)
	}

	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", runErr)
	}
}
