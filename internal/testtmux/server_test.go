//go:build integration

package testtmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/internal/processinfo"
)

func TestServerCleanupAndIsolation(t *testing.T) {
	executable := Executable(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g @aht-user-config loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	writeRejectingWrapper(t, filepath.Join(bin, "tmux"))
	t.Setenv("AHT_TEST_TMUX_EXECUTABLE", executable)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/false")

	var server *Server
	t.Run("owned server", func(t *testing.T) {
		server = New(t, "sleep", "60")
		if got := server.Run(t, "show-option", "-gqv", "@aht-user-config"); got != "" {
			t.Fatalf("test server loaded personal configuration: %q", got)
		}
		if got := server.Run(t, "show-option", "-gv", "default-shell"); got != "/bin/sh\n" {
			t.Fatalf("default shell = %q, want isolated /bin/sh", got)
		}
	})
	if server == nil {
		t.Fatal("server did not start")
	}
	if exited, err := server.exited(t.Context()); err != nil || !exited {
		t.Fatalf("test server survived cleanup: exited=%v, error=%v", exited, err)
	}
	if _, err := os.Stat(server.directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test socket directory survived successful cleanup: %v", err)
	}
}

func TestCloseReportsBlockedCleanup(t *testing.T) {
	server := New(t, "sleep", "60")
	executable := server.executable
	wrapper := filepath.Join(t.TempDir(), "blocked-tmux")
	writeRejectingWrapper(t, wrapper)
	server.executable = wrapper
	defer func() { server.executable = executable }()

	if err := server.Close(t.Context()); err == nil {
		t.Fatal("blocked cleanup was reported as successful")
	}
	if _, err := os.Stat(server.Socket); err != nil {
		t.Fatalf("failed cleanup lost the server socket: %v", err)
	}
	if identity := processinfo.StartIdentity(t.Context(), server.pid); identity != server.startIdentity {
		t.Fatal("failure fixture did not leave the original server alive")
	}
}

func TestCloseDoesNotTreatCanceledInspectionAsExit(t *testing.T) {
	server := New(t, "sleep", "60")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := server.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close with canceled context = %v, want cancellation", err)
	}
}

func writeRejectingWrapper(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'blocked by personal tmux wrapper' >&2\nexit 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
