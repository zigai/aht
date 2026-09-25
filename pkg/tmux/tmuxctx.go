package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	// ErrNoTmuxContext is returned when no tmux environment is available.
	ErrNoTmuxContext     = errors.New("not inside tmux")
	errMissingTmuxPaneID = errors.New("missing tmux pane id")
)

type Pane struct {
	Tmux           registry.Location
	ServerIdentity string
	PanePID        int
	PaneTTY        string
}

type Env struct {
	TMUX     string
	TMUXPane string
}

// ServerProcess is the current-user process snapshot used as a fallback to
// discover custom tmux sockets outside gotmux's standard directories.
type ServerProcess struct {
	PID  int
	Args []string
}

// ServerProcessLister supplies current-user tmux server processes.
type ServerProcessLister func(context.Context) ([]ServerProcess, error)

// ListOptions controls pane discovery and is primarily useful for observer
// injection and deterministic tests.
type ListOptions struct {
	Env             Env
	ServerProcesses ServerProcessLister
	// SocketPaths overrides gotmux's standard socket discovery when non-nil.
	// An empty slice disables it for deterministic tests or explicit discovery.
	SocketPaths []string
}

// ToMuxPane converts a native tmux Pane to a unified mux.Pane.
func (p Pane) ToMuxPane() mux.Pane {
	location := p.Tmux
	if location.ServerID == "" {
		location.ServerID = p.ServerIdentity
	}
	location.PanePID = p.PanePID
	location.PaneTTY = p.PaneTTY
	processes := make([]mux.ProcessRef, 0, 1)
	if p.PanePID > 0 {
		processes = append(processes, mux.ProcessRef{PID: p.PanePID, ProcessGroupID: 0, Command: "", CWD: ""})
	}
	return mux.Pane{
		Location:    location,
		Processes:   processes,
		ProcessTTY:  p.PaneTTY,
		Command:     "",
		CWD:         location.PaneCurrentPath,
		Title:       "",
		Activity:    nil,
		StateReason: "",
	}
}

func Current(ctx context.Context) (registry.Location, error) {
	return CurrentWithEnv(ctx, Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")})
}

func CurrentWithEnv(ctx context.Context, env Env) (registry.Location, error) {
	if err := ctx.Err(); err != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, fmt.Errorf("current tmux context: %w", err)
	}
	if env.TMUX == "" && env.TMUXPane == "" {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, ErrNoTmuxContext
	}

	info, err := gotmux.CurrentWithEnv(ctx, gotmux.Environment{TMUX: env.TMUX, TMUXPane: env.TMUXPane})
	if err == nil {
		return contextFromCurrentInfo(info, tmuxServerSocket(env.TMUX)), nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, fmt.Errorf("current tmux context: %w", contextErr)
	}
	if paneID := env.TMUXPane; paneID != "" {
		return ContextFromEnv(env), nil
	}

	return registry.Location{Kind: "", ServerID: "", SessionID: "", SessionName: "", WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "", WindowID: "", WindowIndex: "", WindowName: "", PaneID: "", PaneIndex: "", PaneCurrentPath: "", PanePID: 0, PaneTTY: "", ClientTTY: ""}, fmt.Errorf("current tmux context: %w", err)
}

func ContextFromEnv(env Env) registry.Location {
	if env.TMUX == "" && env.TMUXPane == "" {
		var tmux registry.Location

		return tmux
	}

	return registry.Location{
		WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "",
		Kind:            registry.MultiplexerTmux,
		ServerID:        tmuxServerSocket(env.TMUX),
		SessionID:       "",
		SessionName:     "",
		WindowID:        "",
		WindowIndex:     "",
		WindowName:      "",
		PaneID:          env.TMUXPane,
		PaneIndex:       "",
		PaneCurrentPath: "",
		PanePID:         0,
		PaneTTY:         "",
		ClientTTY:       "",
	}
}

func ListPanes(ctx context.Context) ([]Pane, error) {
	return ListPanesWithOptions(ctx, ListOptions{ //nolint:exhaustruct_v5 // nil discovery options use defaults
		Env: Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")},
	})
}

func ListPanesWithOptions(ctx context.Context, options ListOptions) ([]Pane, error) {
	env := options.Env
	currentServerSocket := tmuxServerSocket(env.TMUX)
	if options.ServerProcesses == nil {
		options.ServerProcesses = listCurrentUserTmuxServers
	}

	servers, err := discoverServers(ctx, options)
	if err != nil {
		return nil, err
	}
	var panes []Pane
	var firstErr error
	seenPanes := make(map[string]struct{})
	for _, server := range servers {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("list tmux panes: %w", err)
		}
		serverPanes, queryErr := queryServerPanesGotmux(ctx, server)
		if queryErr != nil {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("list tmux panes: %w", err)
			}
			if server.Identity == currentServerSocket && firstErr == nil {
				firstErr = queryErr
			}
			continue
		}
		panes = appendCanonicalPanes(panes, serverPanes, server.Identity, seenPanes)
	}
	if len(panes) == 0 && firstErr != nil {
		return nil, firstErr
	}

	return panes, nil
}

// SendInterruptTo sends an interrupt to a pane on the identified tmux server.
func SendInterruptTo(ctx context.Context, serverIdentity, paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return errMissingTmuxPaneID
	}
	cfg, err := gotmuxConfigForIdentity(serverIdentity)
	if err != nil {
		return err
	}
	server, err := gotmux.New(cfg)
	if err != nil {
		return fmt.Errorf("init tmux server: %w", err)
	}
	pane, err := server.PaneHandle(gotmux.PaneID(paneID))
	if err != nil {
		return fmt.Errorf("resolve tmux pane %s: %w", paneID, err)
	}
	if err := pane.SendKeys(ctx, gotmux.KeyCtrlC); err != nil {
		return fmt.Errorf("send tmux interrupt: %w", err)
	}
	return nil
}

func resolveContextSession(info gotmux.CurrentInfo) (string, string) {
	if session, ok := info.Session.Get(); ok {
		return string(session.ID), session.Name
	}
	id, _ := info.Pane.SessionID.Get()
	name, _ := info.Pane.SessionName.Get()
	return string(id), name
}

func resolveContextWindow(info gotmux.CurrentInfo) (string, string, string) {
	windowID := string(info.Pane.WindowID)
	if windowID == "" {
		windowID = string(info.Window.ID)
	}
	index := ""
	name := info.Window.Name
	if link, ok := info.Link.Get(); ok {
		index = strconv.Itoa(link.Index)
		if name == "" {
			name = link.WindowName
		}
	} else if windex, ok := info.Pane.WindowIndex.Get(); ok {
		index = strconv.Itoa(windex)
	}
	if wname, ok := info.Pane.WindowName.Get(); ok && name == "" {
		name = wname
	}
	return windowID, index, name
}

func contextFromCurrentInfo(info gotmux.CurrentInfo, fallbackSocket string) registry.Location {
	sessionID, sessionName := resolveContextSession(info)
	windowID, windowIndex, windowName := resolveContextWindow(info)

	clientTTY := ""
	if client, ok := info.Client.Get(); ok {
		clientTTY = client.TTY
	}

	serverSocket := info.Identity.ReportedSocket
	if serverSocket == "" {
		serverSocket = info.Identity.Endpoint.SocketPath
	}
	if serverSocket == "" {
		serverSocket = fallbackSocket
	}

	return registry.Location{
		WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "",
		Kind:            registry.MultiplexerTmux,
		ServerID:        serverSocket,
		SessionID:       sessionID,
		SessionName:     sessionName,
		WindowID:        windowID,
		WindowIndex:     windowIndex,
		WindowName:      windowName,
		PaneID:          string(info.Pane.ID),
		PaneIndex:       strconv.Itoa(info.Pane.Index),
		PaneCurrentPath: info.Pane.CurrentPath,
		PanePID:         info.Pane.PID,
		PaneTTY:         info.Pane.TTY,
		ClientTTY:       clientTTY,
	}
}

func paneFromGotmux(p gotmux.PaneInfo, fallbackIdentity string) Pane {
	sessionID := ""
	if sid, ok := p.SessionID.Get(); ok {
		sessionID = string(sid)
	}
	sessionName := ""
	if sname, ok := p.SessionName.Get(); ok {
		sessionName = sname
	}
	windowIndex := ""
	if windex, ok := p.WindowIndex.Get(); ok {
		windowIndex = strconv.Itoa(windex)
	}
	windowName := ""
	if wname, ok := p.WindowName.Get(); ok {
		windowName = wname
	}
	serverIdentity := p.Handle().Identity().ReportedSocket
	if serverIdentity == "" {
		serverIdentity = fallbackIdentity
	}
	return Pane{
		Tmux: registry.Location{
			WorkspaceID: "", WorkspaceName: "", TabID: "", TabIndex: "", TabName: "",
			Kind:            registry.MultiplexerTmux,
			ServerID:        serverIdentity,
			SessionID:       sessionID,
			SessionName:     sessionName,
			WindowID:        string(p.WindowID),
			WindowIndex:     windowIndex,
			WindowName:      windowName,
			PaneID:          string(p.ID),
			PaneIndex:       strconv.Itoa(p.Index),
			PaneCurrentPath: p.CurrentPath,
			PanePID:         p.PID,
			PaneTTY:         p.TTY,
			ClientTTY:       "",
		},
		ServerIdentity: serverIdentity,
		PanePID:        p.PID,
		PaneTTY:        p.TTY,
	}
}

func queryServerPanesGotmux(ctx context.Context, server serverSpec) ([]Pane, error) {
	cfg, err := gotmuxConfigForIdentity(server.Identity)
	if err != nil {
		return nil, err
	}
	s, err := gotmux.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("init tmux server: %w", err)
	}
	paneInfos, err := s.Panes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list panes: %w", err)
	}
	serverPanes := make([]Pane, 0, len(paneInfos))
	for _, p := range paneInfos {
		serverPanes = append(serverPanes, paneFromGotmux(p, server.Identity))
	}
	return serverPanes, nil
}

func appendCanonicalPanes(panes, serverPanes []Pane, fallbackIdentity string, seen map[string]struct{}) []Pane {
	for _, pane := range serverPanes {
		if pane.ServerIdentity == "" {
			pane.ServerIdentity = fallbackIdentity
		}
		pane.Tmux.ServerID = pane.ServerIdentity
		pane.PanePID = pane.Tmux.PanePID
		pane.PaneTTY = pane.Tmux.PaneTTY
		key := pane.ServerIdentity + "\x00" + pane.Tmux.PaneID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		panes = append(panes, pane)
	}
	return panes
}

func tmuxServerSocket(tmuxEnv string) string {
	tmuxEnv = strings.TrimSpace(tmuxEnv)
	if tmuxEnv == "" {
		return ""
	}
	if hints, err := gotmux.ParseEnvironment(gotmux.Environment{TMUX: tmuxEnv, TMUXPane: ""}); err == nil {
		return hints.SocketPath
	}

	// Preserve the public environment-only fallback for incomplete or invalid
	// $TMUX values that gotmux cannot verify. Strip suffixes from the right to
	// retain commas in socket paths.
	end := strings.LastIndexByte(tmuxEnv, ',')
	if end < 0 {
		return tmuxEnv
	}

	start := strings.LastIndexByte(tmuxEnv[:end], ',')

	if start < 0 {
		return tmuxEnv[:end]
	}

	return tmuxEnv[:start]
}
