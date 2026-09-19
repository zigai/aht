//go:build integration || compatibility

// Package testtmux owns isolated tmux servers used by integration tests.
package testtmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/internal/processinfo"
)

const (
	commandTimeout   = 5 * time.Second
	exitPollInterval = 10 * time.Millisecond
)

// Server owns a private socket and the process started for a test.
type Server struct {
	Socket        string
	Tmux          *gotmux.Server
	Session       gotmux.Session
	directory     string
	executable    string
	environment   []string
	pid           int
	startIdentity string
	identity      gotmux.ServerIdentity
}

// Executable bypasses personal PATH wrappers for test server lifecycle commands.
// AHT_TEST_TMUX_EXECUTABLE can select a different installed tmux binary.
func Executable(t *testing.T) string {
	t.Helper()
	if override := os.Getenv("AHT_TEST_TMUX_EXECUTABLE"); override != "" {
		path, err := exec.LookPath(override)
		if err != nil {
			t.Fatalf("resolve test tmux executable: %v", err)
		}
		return path
	}
	for _, candidate := range []string{"/usr/local/bin/tmux", "/opt/homebrew/bin/tmux", "/usr/bin/tmux", "tmux"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("tmux is not installed")
	return ""
}

// New starts a detached session without loading personal tmux or shell config.
func New(t *testing.T, options gotmux.NewSessionOptions) *Server {
	t.Helper()
	return newServer(t, "", options)
}

// NewNamed starts through -L so tests can exercise named-server discovery.
func NewNamed(t *testing.T, name string, options gotmux.NewSessionOptions) *Server {
	t.Helper()
	return newServer(t, name, options)
}

func newServer(t *testing.T, name string, options gotmux.NewSessionOptions) *Server {
	t.Helper()
	executable := Executable(t)
	// Unix socket paths must stay short. Cleanup owns this directory so a
	// failed server shutdown retains its socket for inspection and recovery.
	directory, err := os.MkdirTemp("/tmp", "aht-test-tmux-") //nolint:usetesting // reason: Unix domain socket paths in /tmp must stay short to prevent sockaddr_un overflow
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{ //nolint:exhaustruct_v5 // handles and identities are populated after startup
		Socket:        filepath.Join(directory, "tmux.sock"),
		Tmux:          nil,
		directory:     directory,
		executable:    executable,
		environment:   nil,
		pid:           0,
		startIdentity: "",
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "TMUX" && key != "TMUX_PANE" && key != "SHELL" && key != "TMUX_TMPDIR" {
			server.environment = append(server.environment, value)
		}
	}
	server.environment = append(server.environment, "SHELL=/bin/sh", "TMUX_TMPDIR="+directory)
	if name != "" {
		server.Socket = filepath.Join(directory, fmt.Sprintf("tmux-%d", os.Getuid()), name)
		t.Setenv("TMUX_TMPDIR", directory)
	}
	t.Cleanup(func() { server.cleanup(t) })
	ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
	defer cancel()
	server.start(ctx, t, name, options)
	return server
}

// Close stops the owned server and verifies its process exited before returning.
func (server *Server) Close(ctx context.Context) error {
	exited, err := server.exited(ctx)
	if err != nil {
		return err
	}
	if exited {
		return nil
	}
	if server.pid == 0 {
		if _, err := os.Stat(server.Socket); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err := server.killTmuxServer(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()
	for server.pid != 0 {
		exited, err := server.exited(ctx)
		if err != nil {
			return err
		}
		if exited {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for test tmux exit: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func (server *Server) killTmuxServer(ctx context.Context) error {
	if server.executable != "" {
		cfg := gotmux.Config{ //nolint:exhaustruct_v5 // remaining options default
			Binary:     server.executable,
			SocketPath: server.Socket,
			ConfigFile: "/dev/null",
			Env:        server.environment,
		}
		if server.Tmux != nil {
			endpoint := server.Tmux.Endpoint()
			cfg.SocketPath = endpoint.SocketPath
			cfg.SocketName = endpoint.SocketName
		}
		s, err := gotmux.New(cfg)
		if err != nil {
			return fmt.Errorf("init test tmux cleanup: %w", err)
		}
		server.Tmux = s
	}
	// Startup may have failed before returning a verified handle. Probe only
	// our private socket in that case so partial startup is still cleaned up.
	if server.identity.PID == 0 {
		probe, err := server.Tmux.Probe(ctx)
		if errors.Is(err, gotmux.ErrNoServer) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("probe test tmux cleanup: %w", err)
		}
		server.identity = probe.Identity
		server.pid = server.identity.PID
		server.startIdentity = processinfo.StartIdentity(ctx, server.pid)
	}
	if err := server.Tmux.KillIfIdentity(ctx, server.identity); err != nil && !errors.Is(err, gotmux.ErrNoServer) {
		return fmt.Errorf("stop test tmux: %w", err)
	}
	return nil
}

func (server *Server) start(ctx context.Context, t *testing.T, name string, options gotmux.NewSessionOptions) {
	t.Helper()
	gotmuxConfig := gotmux.Config{ //nolint:exhaustruct_v5 // remaining options default
		Binary:     server.executable,
		SocketPath: server.Socket,
		ConfigFile: "/dev/null",
		Env:        server.environment,
	}
	if name != "" {
		gotmuxConfig.SocketPath = ""
		gotmuxConfig.SocketName = name
	}
	gotmuxServer, err := gotmux.New(gotmuxConfig)
	if err != nil {
		t.Fatalf("init gotmux server: %v", err)
	}
	server.Tmux = gotmuxServer

	options.Start = gotmux.AllowStart
	session, err := server.Tmux.NewSession(ctx, options)
	if session.Valid() {
		server.Session = session
		server.identity = session.Identity()
		server.pid = server.identity.PID
		server.startIdentity = processinfo.StartIdentity(ctx, server.pid)
	}
	if err != nil {
		t.Fatalf("start test tmux: %v", err)
	}
	if server.pid <= 0 {
		t.Fatalf("invalid test tmux server PID: %d", server.pid)
	}
	if server.startIdentity == "" {
		t.Fatal("test tmux server has no process identity")
	}
}

func (server *Server) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		t.Errorf("clean up test tmux server (socket retained at %s): %v", server.Socket, err)
		return
	}
	if err := os.RemoveAll(server.directory); err != nil {
		t.Errorf("remove test tmux directory: %v", err)
	}
}

func (server *Server) exited(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("inspect test tmux exit: %w", err)
	}
	if server.pid == 0 {
		return false, nil
	}
	process, found, err := processinfo.Find(ctx, server.pid)
	if err != nil {
		return false, fmt.Errorf("inspect test tmux process: %w", err)
	}
	return !found || (server.startIdentity != "" && process.StartIdentity != server.startIdentity), nil
}
