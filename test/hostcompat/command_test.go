//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const compatibilityWaitDelay = time.Second

func buildCompatibilityOracle(t *testing.T) string {
	t.Helper()
	oracle := filepath.Join(t.TempDir(), "aht-compat-oracle")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.Command("go", "build", "-ldflags", "-X github.com/zigai/aht/v2/internal/cli.version=compat-oracle", "-o", oracle, ".")
	command.Dir = filepath.Clean(filepath.Join(sourceDirectory(), "..", ".."))
	if output, err := compatibilityOutput(ctx, command); err != nil {
		t.Fatalf("building compatibility oracle: %v\n%s", err, output)
	}
	return oracle
}

func stageCompatibilityOracle(t *testing.T, oracle, target string) {
	t.Helper()
	source, err := os.Open(oracle)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	}()
	// Copies keep scenario cleanup and accidental writes independent of the
	// parent's immutable artifact, including across temporary filesystems.
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	if err := errors.Join(copyErr, destination.Close()); err != nil {
		t.Fatal(err)
	}
}

func compatibilityOutput(ctx context.Context, command *exec.Cmd) ([]byte, error) {
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := runCompatibilityCommand(ctx, command)
	return output.Bytes(), err
}

// Synchronous probes own a separate process group. Cancellation reaches that
// group, Wait reaps its leader, and WaitDelay bounds inherited output pipes.
// Native interactive drivers retain their separate transport/lifecycle owners.
func runCompatibilityCommand(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("compatibility command canceled before start: %w", err)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = compatibilityWaitDelay
	if err := command.Start(); err != nil {
		return fmt.Errorf("start compatibility command: %w", err)
	}
	canceled := make(chan struct{})
	var cancelErr error
	stop := context.AfterFunc(ctx, func() {
		cancelErr = killCompatibilityGroup(command)
		close(canceled)
	})
	waitErr := command.Wait()
	if !stop() {
		// Join the cancellation callback before reading its result or returning.
		<-canceled
		waitErr = errors.Join(waitErr, ctx.Err(), cancelErr)
	}
	// A successful leader may still leave children holding its output pipes.
	return errors.Join(waitErr, killCompatibilityGroup(command))
}

func killCompatibilityGroup(command *exec.Cmd) error {
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill compatibility process group: %w", err)
	}
	return nil
}
