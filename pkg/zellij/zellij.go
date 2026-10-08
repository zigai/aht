package zellij

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/command"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

const defaultCaptureLines = 100

var errPaneRequired = errors.New("zellij session and pane are required")

type Env struct {
	SessionName string
	PaneID      string
}

// CommandRunner runs zellij with args. A runner should pass [SocketDir] of ctx
// as ZELLIJ_SOCKET_DIR so the command reaches only the targeted session.
type CommandRunner func(ctx context.Context, args ...string) (string, error)

type socketDirKey struct{}

// ListOptions configures [ListPanesWithOptions]. Empty directories and a nil
// Now resolve like Zellij does: from the environment and the system clock.
type ListOptions struct {
	Run            CommandRunner
	LookPath       func(string) (string, error)
	SocketDir      string
	SessionInfoDir string
	Now            func() time.Time
}

// CaptureOptions configures [CapturePaneWithOptions].
type CaptureOptions struct {
	Run       CommandRunner
	SocketDir string
}

type paneRecord struct {
	ID          uint32 `json:"id"`
	IsPlugin    bool   `json:"is_plugin"`
	Title       string `json:"title"`
	Exited      bool   `json:"exited"`
	TabID       int    `json:"tab_id"`
	TabPosition int    `json:"tab_position"`
	TabName     string `json:"tab_name"`
	PaneCommand string `json:"pane_command"`
	PaneCWD     string `json:"pane_cwd"`
}

// SocketDir returns the Zellij socket directory a [CommandRunner] call is
// scoped to, or "" when the call is not scoped.
func SocketDir(ctx context.Context) string {
	dir, _ := ctx.Value(socketDirKey{}).(string)
	return dir
}

func Current() registry.Location {
	return CurrentWithEnv(Env{SessionName: os.Getenv("ZELLIJ_SESSION_NAME"), PaneID: os.Getenv("ZELLIJ_PANE_ID")})
}

func CurrentWithEnv(env Env) registry.Location {
	if strings.TrimSpace(env.SessionName) == "" || strings.TrimSpace(env.PaneID) == "" {
		var empty registry.Location
		return empty
	}
	return registry.Location{
		ServerID: "", SessionID: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: "",
		Kind: registry.MultiplexerZellij, SessionName: env.SessionName, PaneID: mux.NormalizePaneID(registry.MultiplexerZellij, env.PaneID),
	}
}

// withRunner fills in the zellij runner. It reports false when zellij is not
// installed, which an optional multiplexer treats as having no panes.
func (opts ListOptions) withRunner() (ListOptions, bool) {
	if opts.Run != nil {
		return opts, true
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("zellij"); err != nil {
		return opts, false
	}
	opts.Run = runZellij
	return opts, true
}

func (opts ListOptions) withDefaults() (ListOptions, error) {
	if opts.SocketDir == "" {
		opts.SocketDir = defaultSocketDir()
	}
	if opts.SessionInfoDir == "" {
		dir, err := defaultSessionInfoDir()
		if err != nil {
			return opts, err
		}
		opts.SessionInfoDir = dir
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts, nil
}

func (opts ListOptions) usable(ctx context.Context, socket sessionSocket) (bool, error) {
	if !socket.ready(opts.SessionInfoDir, opts.Now()) {
		return false, nil
	}
	return socket.alive(ctx)
}

func ListPanes(ctx context.Context) ([]mux.Pane, error) {
	return ListPanesWithOptions(ctx, ListOptions{Run: nil, LookPath: nil, SocketDir: "", SessionInfoDir: "", Now: nil})
}

func ListPanesWithOptions(ctx context.Context, opts ListOptions) ([]mux.Pane, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list zellij panes: %w", err)
	}
	opts, available := opts.withRunner()
	if !available {
		return nil, nil
	}
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, fmt.Errorf("list zellij sessions: %w", err)
	}
	sockets, err := listSessionSockets(opts.SocketDir)
	if err != nil {
		return nil, fmt.Errorf("list zellij sessions: %w", err)
	}
	panes := make([]mux.Pane, 0)
	var listErrors []error
	for _, socket := range sockets {
		usable, usableErr := opts.usable(ctx, socket)
		if usableErr != nil {
			listErrors = append(listErrors, usableErr)
			continue
		}
		if !usable {
			continue
		}
		output, listErr := runInSession(ctx, opts.Run, opts.SocketDir, socket.name, "--session", socket.name, "action", "list-panes", "--all", "--json")
		if errors.Is(listErr, errSessionGone) {
			continue
		}
		if listErr != nil {
			listErrors = append(listErrors, fmt.Errorf("list zellij session %q panes: %w", socket.name, listErr))
			continue
		}
		parsed, parseErr := parsePanes(socket.name, output)
		if parseErr != nil {
			listErrors = append(listErrors, fmt.Errorf("list zellij session %q panes: %w", socket.name, parseErr))
			continue
		}
		panes = append(panes, parsed...)
	}
	return panes, errors.Join(listErrors...)
}

func CapturePane(ctx context.Context, pane mux.Pane) (mux.ScreenSnapshot, error) {
	return CapturePaneWithOptions(ctx, pane, CaptureOptions{Run: nil, SocketDir: ""})
}

func CapturePaneWithOptions(ctx context.Context, pane mux.Pane, opts CaptureOptions) (mux.ScreenSnapshot, error) {
	if pane.Location.Kind != registry.MultiplexerZellij || pane.Location.SessionName == "" || pane.Location.PaneID == "" {
		return mux.ScreenSnapshot{}, errPaneRequired
	}
	run := opts.Run
	if run == nil {
		run = runZellij
	}
	socketDir := opts.SocketDir
	if socketDir == "" {
		socketDir = defaultSocketDir()
	}
	text, err := runInSession(ctx, run, socketDir, pane.Location.SessionName, "--session", pane.Location.SessionName, "action", "dump-screen", "--pane-id", pane.Location.PaneID)
	if err != nil {
		return mux.ScreenSnapshot{}, fmt.Errorf("capture zellij pane: %w", err)
	}
	return mux.ScreenSnapshot{Text: strings.Join(mux.BoundBottomLines(text, defaultCaptureLines), "\n"), Title: pane.Title}, nil
}

func parsePanes(session string, output string) ([]mux.Pane, error) {
	var records []paneRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		return nil, fmt.Errorf("parse zellij panes: %w", err)
	}
	panes := make([]mux.Pane, 0, len(records))
	for _, record := range records {
		if record.IsPlugin || record.Exited {
			continue
		}
		paneID := "terminal_" + strconv.FormatUint(uint64(record.ID), 10)
		location := registry.Location{
			ServerID: "", SessionID: "", WorkspaceID: "", WorkspaceName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneIndex: "", PanePID: 0, PaneTTY: "", ClientTTY: "",
			Kind: registry.MultiplexerZellij, SessionName: session,
			TabID: strconv.Itoa(record.TabID), TabIndex: strconv.Itoa(record.TabPosition), TabName: record.TabName,
			PaneID: paneID, PaneCurrentPath: record.PaneCWD,
		}
		panes = append(panes, mux.Pane{ //nolint:exhaustruct_v5 // CLI lacks process references and activity
			Location: location, Command: record.PaneCommand, CWD: record.PaneCWD, Title: record.Title,
		})
	}
	return panes, nil
}

func runZellij(ctx context.Context, args ...string) (string, error) {
	env := os.Environ()
	if dir := SocketDir(ctx); dir != "" {
		env = append(env, "ZELLIJ_SOCKET_DIR="+dir)
	}
	output, err := command.Run(ctx, "zellij", env, args...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return string(output), fmt.Errorf("run zellij command: %w", ctxErr)
		}
		return string(output), fmt.Errorf("run zellij command: %w", err)
	}
	return string(output), nil
}
