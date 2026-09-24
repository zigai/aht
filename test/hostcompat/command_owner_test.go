//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestCompatibilityCommandDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	command := compatibilityFixtureCommand(t, "block", "")
	started := time.Now()
	output, err := compatibilityOutput(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked command error = %v, want deadline exceeded; output: %s", err, output)
	}
	if command.ProcessState == nil {
		t.Fatal("timed-out command was not reaped")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("command exceeded its cancellation/drain budget: %s", elapsed)
	}
	if !bytes.Contains(output, []byte("fixture ready")) {
		t.Fatalf("command did not reach its blocking operation: %s", output)
	}
}

func TestCompatibilityCommandInheritedOutput(t *testing.T) {
	t.Parallel()
	work := t.TempDir()
	lockPath := filepath.Join(work, "child.lock")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := compatibilityFixtureCommand(t, "inherited-output", work)
	output, err := compatibilityOutput(ctx, command)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("inherited output error = %v, want pipe-drain timeout; output: %s", err, output)
	}
	if command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("successful group leader was not reaped")
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	// The descendant owns this lock while it holds the inherited stdout pipe.
	// Successful reacquisition proves group cleanup reached that descendant.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return
		} else if !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("descendant survived cancellation of its owned process group")
		case <-ticker.C:
		}
	}
}

func TestCompatibilityOracleStaging(t *testing.T) {
	t.Parallel()
	oracle := filepath.Join(t.TempDir(), "oracle")
	const content = "immutable oracle fixture"
	if err := os.WriteFile(oracle, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contract := hostContract{ID: registry.Harness("pi"), Executable: executable}
	first := newIsolatedHost(t, contract, oracle)
	t.Run("isolated_scenario", func(t *testing.T) {
		second := newIsolatedHost(t, contract, oracle)
		if first.home == second.home || first.work == second.work || first.store == second.store || first.aht == second.aht {
			t.Fatal("scenarios share mutable host paths")
		}
		assertCompatibilityOracleContent(t, second.aht+".real", content)
		if err := os.WriteFile(second.aht+".real", []byte("changed scenario copy"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	for _, path := range []string{oracle, first.aht + ".real"} {
		assertCompatibilityOracleContent(t, path, content)
	}
	info, err := os.Stat(first.aht + ".real")
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("staged oracle is not executable: error = %v", err)
	}
}

func assertCompatibilityOracleContent(t *testing.T, path, content string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("oracle content at %s = %q, error = %v", path, data, err)
	}
}

func compatibilityFixtureCommand(t *testing.T, mode, work string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestCompatibilityCommandFixture$")
	command.Env = append(isolatedEnvironment(), "AHT_COMPAT_COMMAND_FIXTURE="+mode)
	command.Dir = work
	return command
}

func TestCompatibilityCommandFixture(t *testing.T) {
	mode := os.Getenv("AHT_COMPAT_COMMAND_FIXTURE")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	switch mode {
	case "block":
		if _, err := os.Stdout.WriteString("fixture ready\n"); err != nil {
			t.Fatal(err)
		}
		<-time.After(time.Minute)
	case "hold-output":
		holdCompatibilityFixtureOutput(t)
	case "inherited-output":
		command := compatibilityFixtureCommand(t, "hold-output", "")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		// The owner intentionally exits while its child keeps stdout open.
		// The outer command owner must clean up the entire process group.
		defer func() { _ = command.Process.Release() }()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat("child.lock.ready"); err == nil {
				return
			}
			select {
			case <-deadline.C:
				t.Fatal("descendant did not acquire its lock")
			case <-ticker.C:
			}
		}
	default:
		t.Fatalf("unknown command fixture %q", mode)
	}
}

func holdCompatibilityFixtureOutput(t *testing.T) {
	t.Helper()
	lock, err := os.OpenFile("child.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("child.lock.ready", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-time.After(time.Minute)
}
