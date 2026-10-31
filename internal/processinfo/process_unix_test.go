//go:build linux || darwin

package processinfo

import (
	"context"
	"os"
	"testing"
)

func TestCurrentProcessInfo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	pid := os.Getpid()
	if got := StartIdentity(ctx, pid); got == "" {
		t.Fatal("expected current process start identity")
	}

	command, err := CommandName(ctx, pid)
	if err != nil {
		t.Fatalf("current process command name returned error: %v", err)
	}

	if command == "" {
		t.Fatal("expected current process command name")
	}

	process, found, err := Find(ctx, pid)
	if err != nil {
		t.Fatalf("finding current process returned error: %v", err)
	}

	if !found {
		t.Fatal("current process was not found")
	}

	if process.PID != pid || process.StartIdentity == "" || process.Executable == "" {
		t.Fatalf("current process = %#v, want complete identity", process)
	}

	if got := StartIdentity(ctx, pid); got != process.StartIdentity {
		t.Fatalf("StartIdentity = %q, Find identity = %q", got, process.StartIdentity)
	}
}
