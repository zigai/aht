package zellij

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

	scopePrefix   = "a"
	scopeNameBase = 36

	// maxUnixSocketPath is the longest socket path Zellij accepts: sun_path
	// less the terminating NUL byte.
	//
	//nolint:exhaustruct_v5 // reads only the array length
	maxUnixSocketPath = len(syscall.RawSockaddrUnix{}.Path) - 1
)

var (
	errSessionGone     = errors.New("zellij session is gone")
	errSessionStarting = errors.New("zellij session is starting")
	errScopeTooLong    = errors.New("zellij session scope exceeds the socket path limit")
)

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
		// Names starting with a dot belong to Zellij's own app sessions.
		if entry.Type()&fs.ModeSocket == 0 || entry.Name() == webServerSocket ||
			strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				continue
			}

			return nil, fmt.Errorf("stat zellij socket: %w", infoErr)
		}

		sockets = append(
			sockets,
			sessionSocket{
				name:    entry.Name(),
				path:    filepath.Join(dir, entry.Name()),
				created: info.ModTime(),
			},
		)
	}

	return sockets, nil
}

// sessionName returns the name of the session whose socket is named id. Since
// Zellij 0.46, sockets and session metadata folders are named after a session
// id, and only the metadata records the session name. Earlier versions name
// both after the session. Like Zellij, it falls back to the id when the
// metadata does not name the session.
func sessionName(sessionInfoDir string, id string) string {
	file, err := os.Open(filepath.Join(sessionInfoDir, id, sessionMetadata))
	if err != nil {
		return id
	}
	defer file.Close() //nolint:errcheck // read-only file

	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return id
	}

	quoted, found := strings.CutPrefix(strings.TrimSpace(line), "name ")
	if !found {
		return id
	}

	name, err := strconv.Unquote(quoted)
	if err != nil || name == "" {
		return id
	}

	return name
}

// findSessionSocket returns the socket of the session called name.
func findSessionSocket(
	socketDir string,
	sessionInfoDir string,
	name string,
) (sessionSocket, error) {
	sockets, err := listSessionSockets(socketDir)
	if err != nil {
		return sessionSocket{}, err
	}

	for _, socket := range sockets {
		if socket.name == name || sessionName(sessionInfoDir, socket.name) == name {
			return socket, nil
		}
	}

	return sessionSocket{}, fmt.Errorf("%w: %q", errSessionGone, name)
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

// runInSession runs a zellij command with a socket directory that holds only
// the given session's socket. Every Zellij command, including list-sessions and
// --session <name> action, connects to every socket in its socket directory,
// so a shared directory would expose sessions that are still starting.
func (opts ListOptions) runInSession(
	ctx context.Context,
	session string,
	args ...string,
) (string, error) {
	scope, err := opts.scopeSession(session)
	if err != nil {
		return "", err
	}

	if scope == "" {
		return opts.Run(context.WithValue(ctx, socketDirKey{}, opts.SocketDir), args...)
	}

	defer os.RemoveAll( //nolint:errcheck // the scope holds only a hard link and an empty directory
		scope,
	)

	return opts.Run(context.WithValue(ctx, socketDirKey{}, scope), args...)
}

// scopeSession links the session socket into a new scope directory and
// returns it. The linked socket path must stay within maxUnixSocketPath, and
// the hard link within the socket's file system, so the scope goes in the
// socket directory, its parent, or the temporary directory, whichever fits
// first. When none fits, it returns "" so the command runs in the shared
// socket directory, which it allows only while no other session is starting.
func (opts ListOptions) scopeSession(session string) (string, error) {
	source := filepath.Join(opts.SocketDir, contractDir, session)

	var scopeErrors []error

	for _, parent := range scopeParents(opts.SocketDir) {
		scope, err := linkScope(parent, source, session)
		if err == nil {
			return scope, nil
		}

		if errors.Is(err, errSessionGone) {
			return "", err
		}

		scopeErrors = append(scopeErrors, err)
	}

	if _, err := os.Lstat(source); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %q", errSessionGone, session)
		}

		return "", fmt.Errorf("stat zellij session %q socket: %w", session, err)
	}

	if err := opts.othersReady(session); err != nil {
		return "", fmt.Errorf(
			"scope zellij session %q: %w",
			session,
			errors.Join(append(scopeErrors, err)...),
		)
	}

	return "", nil
}

func scopeParents(socketDir string) []string {
	var parents []string

	for _, dir := range []string{socketDir, filepath.Dir(socketDir), os.TempDir()} {
		dir = filepath.Clean(dir)
		if !slices.Contains(parents, dir) {
			parents = append(parents, dir)
		}
	}

	return parents
}

// linkScope creates the shortest free scope directory in parent, so the linked
// socket path fits whenever any scope in parent can.
func linkScope(parent string, source string, session string) (string, error) {
	for index := 0; ; index++ {
		scope := filepath.Join(parent, scopePrefix+strconv.FormatInt(int64(index), scopeNameBase))
		if len(filepath.Join(scope, contractDir, session)) > maxUnixSocketPath {
			return "", fmt.Errorf("%w: %q", errScopeTooLong, parent)
		}

		err := os.Mkdir(scope, 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return "", fmt.Errorf("create zellij session scope: %w", err)
		}

		if err := linkSocket(scope, source, session); err != nil {
			_ = os.RemoveAll(scope)

			return "", err
		}

		return scope, nil
	}
}

func linkSocket(scope string, source string, session string) error {
	scoped := filepath.Join(scope, contractDir)
	if err := os.Mkdir(scoped, 0o700); err != nil {
		return fmt.Errorf("create zellij session scope: %w", err)
	}

	if err := os.Link(source, filepath.Join(scoped, session)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %q", errSessionGone, session)
		}

		return fmt.Errorf("link zellij session %q socket: %w", session, err)
	}

	return nil
}

func (opts ListOptions) othersReady(session string) error {
	sockets, err := listSessionSockets(opts.SocketDir)
	if err != nil {
		return err
	}

	now := opts.Now()
	for _, socket := range sockets {
		if socket.name != session && !socket.ready(opts.SessionInfoDir, now) {
			return fmt.Errorf("%w: %q", errSessionStarting, socket.name)
		}
	}

	return nil
}
