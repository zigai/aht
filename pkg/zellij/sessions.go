package zellij

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

const (
	contractDir        = "contract_version_1"
	webServerSocket    = "web_server_bus"
	sessionMetadata    = "session-metadata.kdl"
	darwinCacheDirName = "org.Zellij-Contributors.Zellij"

	// startupGrace bounds how long a session without a metadata file is
	// treated as starting. Zellij finishes initializing a session within
	// roughly 20ms, and about 120ms with the CPUs oversubscribed threefold.
	startupGrace = 10 * time.Second

	scopePrefix = "a"
)

var errSessionGone = errors.New("zellij session is gone")

type sessionSocket struct {
	name    string
	path    string
	created time.Time
}

// defaultSocketDir resolves the directory Zellij reads from ZELLIJ_SOCKET_DIR,
// matching zellij-utils/src/consts.rs at v0.45.1.
func defaultSocketDir() string {
	if dir := os.Getenv("ZELLIJ_SOCKET_DIR"); dir != "" {
		return dir
	}
	if runtime.GOOS != "darwin" {
		if dir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
			return filepath.Join(dir, "zellij")
		}
	}
	return filepath.Join(os.TempDir(), "zellij-"+strconv.Itoa(os.Getuid()))
}

// defaultSessionInfoDir resolves the directory where Zellij writes per-session
// metadata, matching zellij-utils/src/consts.rs at v0.45.1.
func defaultSessionInfoDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve zellij cache dir: %w", err)
	}
	name := "zellij"
	if runtime.GOOS == "darwin" {
		name = darwinCacheDirName
	}
	return filepath.Join(cache, name, contractDir, "session_info"), nil
}

// listSessionSockets returns the session sockets Zellij has bound, without
// connecting to any of them.
func listSessionSockets(socketDir string) ([]sessionSocket, error) {
	dir := filepath.Join(socketDir, contractDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read zellij socket dir: %w", err)
	}
	var sockets []sessionSocket
	for _, entry := range entries {
		if entry.Type()&fs.ModeSocket == 0 || entry.Name() == webServerSocket {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat zellij socket: %w", infoErr)
		}
		sockets = append(sockets, sessionSocket{name: entry.Name(), path: filepath.Join(dir, entry.Name()), created: info.ModTime()})
	}
	return sockets, nil
}

// ready reports whether the session server has finished initializing.
//
// The server binds its socket before it creates the session state, and a
// client that connects and disconnects in between makes the server panic. The
// session metadata file is written only after the session state exists, so a
// metadata file at least as new as the socket proves the session is ready.
// Metadata can be disabled by configuration, so a session also counts as ready
// once its socket is older than startupGrace.
func (s sessionSocket) ready(sessionInfoDir string, now time.Time) bool {
	if now.Sub(s.created) >= startupGrace {
		return true
	}
	info, err := os.Stat(filepath.Join(sessionInfoDir, s.name, sessionMetadata))
	return err == nil && !info.ModTime().Before(s.created)
}

// alive reports whether a server still accepts connections on the socket. It
// must only be called on a ready session.
func (s sessionSocket) alive(ctx context.Context) (bool, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", s.path)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil
		}
		return false, fmt.Errorf("connect to zellij session %q: %w", s.name, err)
	}
	if err := conn.Close(); err != nil {
		return false, fmt.Errorf("close zellij session %q connection: %w", s.name, err)
	}
	return true, nil
}

// runInSession runs run with a socket directory that holds only the given
// session's socket. Every Zellij command, including list-sessions and
// --session <name> action, connects to every socket in its socket directory,
// so a shared directory would expose sessions that are still starting.
func runInSession(ctx context.Context, run CommandRunner, socketDir string, session string, args ...string) (string, error) {
	scope, err := os.MkdirTemp(socketDir, scopePrefix)
	if err != nil {
		return "", fmt.Errorf("create zellij session scope: %w", err)
	}
	defer os.RemoveAll(scope) //nolint:errcheck // the scope holds only a hard link and an empty directory
	scoped := filepath.Join(scope, contractDir)
	if err := os.Mkdir(scoped, 0o700); err != nil {
		return "", fmt.Errorf("create zellij session scope: %w", err)
	}
	if err := os.Link(filepath.Join(socketDir, contractDir, session), filepath.Join(scoped, session)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %q", errSessionGone, session)
		}
		return "", fmt.Errorf("link zellij session %q socket: %w", session, err)
	}
	return run(context.WithValue(ctx, socketDirKey{}, scope), args...)
}
