package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gotmux "github.com/zigai/gotmux/tmux"
)

var (
	errNoTmux         = errors.New("tmux is not installed")
	errNoTerminalArgv = errors.New("terminal program is empty")
	errTerminalPanes  = errors.New("terminal session must have exactly one pane")
	errEmptyText      = errors.New("terminal text must not be empty")
)

// Terminal is a harness running in a private tmux server.
type Terminal struct {
	server    *gotmux.Server
	pane      gotmux.Pane
	tmux      string
	socket    string
	directory string
	closed    bool
}

// StartTerminal starts the terminal UI in a private tmux server.
func (h *Harness) StartTerminal(
	ctx context.Context,
	prompt string,
	size gotmux.Size,
) (*Terminal, error) {
	args, err := h.InteractiveArgs(prompt)
	if err != nil {
		return nil, err
	}

	envPath, err := exec.LookPath("env")
	if err != nil {
		return nil, fmt.Errorf("resolve env: %w", err)
	}

	argv := append([]string{envPath, "-i"}, h.Env...)
	argv = append(argv, h.Executable)
	argv = append(argv, args...)

	return startTerminal(ctx, argv, h.Work, size)
}

// Pane returns the pane that runs the harness.
func (t *Terminal) Pane() gotmux.Pane {
	return t.pane
}

// Socket returns the path of the private tmux socket.
func (t *Terminal) Socket() string {
	return t.socket
}

// AttachCommand returns the argv that attaches to the terminal.
func (t *Terminal) AttachCommand() []string {
	return []string{t.tmux, "-S", t.socket, "attach"}
}

// Capture returns the visible text of the pane.
func (t *Terminal) Capture(ctx context.Context) (string, error) {
	screen, err := t.pane.Capture(ctx, gotmux.CaptureOptions{})
	if err != nil {
		return "", fmt.Errorf("capture terminal: %w", err)
	}

	return string(screen), nil
}

// SendText types text literally, without a trailing Enter.
func (t *Terminal) SendText(ctx context.Context, text string) error {
	if err := t.pane.SendText(ctx, text); err != nil {
		return fmt.Errorf("send text to terminal: %w", err)
	}

	return nil
}

// SendKeys sends named keys to the pane.
func (t *Terminal) SendKeys(ctx context.Context, keys ...gotmux.Key) error {
	if err := t.pane.SendKeys(ctx, keys...); err != nil {
		return fmt.Errorf("send keys to terminal: %w", err)
	}

	return nil
}

// WaitForText waits until the pane shows text.
func (t *Terminal) WaitForText(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", errEmptyText
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		screen, err := t.Capture(ctx)
		if err != nil && ctx.Err() == nil {
			return "", err
		}

		if err == nil && strings.Contains(screen, text) {
			return screen, nil
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf(
				"wait for terminal text %q: %w; last capture:\n%s",
				text,
				ctx.Err(),
				screen,
			)
		case <-ticker.C:
		}
	}
}

// Exited reports whether the harness process in the pane has exited.
func (t *Terminal) Exited(ctx context.Context) (bool, error) {
	info, err := t.pane.Info(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect terminal pane: %w", err)
	}

	return info.Dead, nil
}

// Close stops the tmux server.
func (t *Terminal) Close(ctx context.Context) error {
	if t.closed {
		return nil
	}

	if err := t.server.Kill(ctx); err != nil && !errors.Is(err, gotmux.ErrNoServer) {
		return fmt.Errorf("stop terminal server (socket %s): %w", t.socket, err)
	}

	if err := t.waitForExit(ctx); err != nil {
		return fmt.Errorf("terminal server did not exit (socket %s): %w", t.socket, err)
	}

	if err := os.RemoveAll(t.directory); err != nil {
		return fmt.Errorf("remove terminal directory: %w", err)
	}

	t.closed = true

	return nil
}

func (t *Terminal) start(ctx context.Context, argv []string, dir string, size gotmux.Size) error {
	session, err := t.server.NewSession(ctx, gotmux.NewSessionOptions{
		Name:    "harness",
		Dir:     dir,
		Size:    size,
		Program: gotmux.Exec("/bin/sh"),
	})
	if err != nil {
		return fmt.Errorf("start terminal session: %w", err)
	}

	panes, err := session.Panes(ctx)
	if err != nil {
		return fmt.Errorf("list terminal panes: %w", err)
	}

	if len(panes) != 1 {
		return fmt.Errorf("%w: found %d", errTerminalPanes, len(panes))
	}

	t.pane = panes[0].Handle()
	if err := t.pane.Options().SetRemainOnExit(ctx, gotmux.RemainOn); err != nil {
		return fmt.Errorf("keep terminal pane after exit: %w", err)
	}

	if err := t.pane.Respawn(ctx, gotmux.RespawnOptions{
		Dir:         dir,
		Program:     gotmux.Exec(argv[0], argv[1:]...),
		KillRunning: true,
	}); err != nil {
		return fmt.Errorf("start terminal program: %w", err)
	}

	return nil
}

func (t *Terminal) waitForExit(ctx context.Context) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		_, err := t.server.Probe(ctx)
		if errors.Is(err, gotmux.ErrNoServer) {
			return nil
		}

		select {
		case <-ctx.Done():
			return errors.Join(fmt.Errorf("wait for terminal server exit: %w", ctx.Err()), err)
		case <-ticker.C:
		}
	}
}

func startTerminal(
	ctx context.Context,
	argv []string,
	dir string,
	size gotmux.Size,
) (*Terminal, error) {
	if len(argv) == 0 {
		return nil, errNoTerminalArgv
	}

	tmuxPath, err := tmuxExecutable()
	if err != nil {
		return nil, err
	}

	directory, err := os.MkdirTemp("/tmp", "aht-launch-tmux-")
	if err != nil {
		return nil, fmt.Errorf("create terminal directory: %w", err)
	}

	socket := filepath.Join(directory, "tmux.sock")

	server, err := gotmux.New(gotmux.Config{
		Binary:     tmuxPath,
		SocketPath: socket,
		ConfigFile: "/dev/null",
		Env:        terminalEnvironment(),
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("configure tmux: %w", err), os.RemoveAll(directory))
	}

	term := &Terminal{
		server:    server,
		pane:      gotmux.Pane{},
		tmux:      tmuxPath,
		socket:    socket,
		directory: directory,
		closed:    false,
	}
	if err := term.start(ctx, argv, dir, size); err != nil {
		return nil, errors.Join(err, term.Close(ctx))
	}

	return term, nil
}

func terminalEnvironment() []string {
	return []string{"LANG=C.UTF-8", "PATH=" + os.Getenv("PATH"), "SHELL=/bin/sh"}
}

func tmuxExecutable() (string, error) {
	if override := os.Getenv("AHT_TEST_TMUX_EXECUTABLE"); override != "" {
		path, err := exec.LookPath(override)
		if err != nil {
			return "", fmt.Errorf("resolve AHT_TEST_TMUX_EXECUTABLE: %w", err)
		}

		return path, nil
	}

	for _, candidate := range []string{"/usr/local/bin/tmux", "/opt/homebrew/bin/tmux", "/usr/bin/tmux", "tmux"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}

	return "", errNoTmux
}
