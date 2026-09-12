package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/shlex"
	gotmux "github.com/zigai/gotmux/tmux"

	"github.com/zigai/aht/pkg/registry"
)

const (
	fieldSeparator     = "\t"
	escapedFieldPrefix = "tmuxctx:"
	listPaneFieldCount = 11
)

var (
	// ErrNoTmuxContext is returned when no tmux environment is available.
	ErrNoTmuxContext = errors.New("not inside tmux")
	// ErrInvalidFieldCount is returned when tmux output does not match the requested format.
	ErrInvalidFieldCount = errors.New("invalid tmux field count")
	errMissingTmuxPaneID = errors.New("missing tmux pane id")
)

type Pane struct {
	Tmux           registry.TmuxContext
	ServerIdentity string
	PanePID        int
	PaneTTY        string
}

type Env struct {
	TMUX     string
	TMUXPane string
}

// ServerProcess is the current-user process snapshot used to discover custom
// tmux servers.
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
}

func Current(ctx context.Context) (registry.TmuxContext, error) {
	return CurrentWithEnv(ctx, Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")})
}

func CurrentWithEnv(ctx context.Context, env Env) (registry.TmuxContext, error) {
	if err := ctx.Err(); err != nil {
		return registry.TmuxContext{}, fmt.Errorf("current tmux context: %w", err)
	}
	if env.TMUX == "" && env.TMUXPane == "" {
		return registry.TmuxContext{}, ErrNoTmuxContext
	}

	info, err := gotmux.CurrentWithEnv(ctx, gotmux.Environment{TMUX: env.TMUX, TMUXPane: env.TMUXPane})
	if err == nil {
		return contextFromCurrentInfo(info, tmuxServerSocket(env.TMUX)), nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return registry.TmuxContext{}, fmt.Errorf("current tmux context: %w", contextErr)
	}
	if paneID := env.TMUXPane; paneID != "" {
		return ContextFromEnv(env), nil
	}

	return registry.TmuxContext{}, fmt.Errorf("current tmux context: %w", err)
}

func ContextFromEnv(env Env) registry.TmuxContext {
	if env.TMUX == "" && env.TMUXPane == "" {
		var tmux registry.TmuxContext

		return tmux
	}

	var serverSocket string
	if hints, err := gotmux.ParseEnvironment(gotmux.Environment{TMUX: env.TMUX, TMUXPane: env.TMUXPane}); err == nil {
		serverSocket = hints.SocketPath
	} else {
		serverSocket = tmuxServerSocket(env.TMUX)
	}

	return registry.TmuxContext{
		Inside:          true,
		ServerSocket:    serverSocket,
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
	return ListPanesWithOptions(ctx, ListOptions{ //nolint:exhaustruct_v5 // nil Run and ServerProcesses use defaults
		Env: Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")},
	})
}

func ListPanesWithOptions(ctx context.Context, options ListOptions) ([]Pane, error) {
	env := options.Env
	currentServerSocket := tmuxServerSocket(env.TMUX)
	if options.ServerProcesses == nil {
		options.ServerProcesses = listCurrentUserTmuxServers
	}

	servers, err := discoverServers(ctx, env, options.ServerProcesses)
	if err != nil {
		return nil, err
	}
	var panes []Pane
	var firstErr error
	seenPanes := make(map[string]struct{})
	for _, server := range servers {
		serverPanes, queryErr := queryServerPanesGotmux(ctx, server)
		if queryErr != nil {
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

func ParseCurrent(output string) (registry.TmuxContext, error) {
	const expectedFields = 11
	fields, err := parseTmuxFields(output, expectedFields)
	if err != nil {
		return registry.TmuxContext{}, err
	}
	if len(fields) != expectedFields {
		return registry.TmuxContext{}, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, expectedFields, len(fields))
	}

	return registry.TmuxContext{
		Inside:          true,
		ServerSocket:    "",
		SessionID:       fields[0],
		SessionName:     fields[1],
		WindowID:        fields[2],
		WindowIndex:     fields[3],
		WindowName:      fields[4],
		PaneID:          fields[5],
		PaneIndex:       fields[6],
		PaneCurrentPath: fields[7],
		PanePID:         parsePositiveInt(fields[8]),
		PaneTTY:         fields[9],
		ClientTTY:       fields[10],
	}, nil
}

func ParseListPanes(output string) ([]Pane, error) {
	trimmed := strings.TrimRight(output, "\r\n")
	if trimmed == "" {
		return nil, nil
	}

	fields, ok, err := parseEscapedFields(trimmed)
	if err != nil {
		return nil, err
	}
	if !ok {
		lines := strings.Split(trimmed, "\n")
		fields = make([]string, 0, len(lines)*listPaneFieldCount)
		for _, line := range lines {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			parts := strings.Split(line, fieldSeparator)
			if len(parts) != listPaneFieldCount {
				return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, listPaneFieldCount, len(parts))
			}
			fields = append(fields, parts...)
		}
	}

	return panesFromFields(fields)
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

func contextFromCurrentInfo(info gotmux.CurrentInfo, fallbackSocket string) registry.TmuxContext {
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

	return registry.TmuxContext{
		Inside:          true,
		ServerSocket:    serverSocket,
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
		Tmux: registry.TmuxContext{
			Inside:          true,
			ServerSocket:    serverIdentity,
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
		pane.Tmux.ServerSocket = pane.ServerIdentity
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

func panesFromFields(fields []string) ([]Pane, error) {
	if len(fields)%listPaneFieldCount != 0 {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, listPaneFieldCount, len(fields))
	}

	panes := make([]Pane, 0, len(fields)/listPaneFieldCount)
	for row := range len(fields) / listPaneFieldCount {
		offset := row * listPaneFieldCount
		paneFields := fields[offset : offset+listPaneFieldCount]
		pane := Pane{
			Tmux: registry.TmuxContext{
				Inside:          true,
				ServerSocket:    "",
				SessionID:       paneFields[0],
				SessionName:     paneFields[1],
				WindowID:        paneFields[2],
				WindowIndex:     paneFields[3],
				WindowName:      paneFields[4],
				PaneID:          paneFields[5],
				PaneIndex:       paneFields[6],
				PaneCurrentPath: paneFields[7],
				PanePID:         parsePositiveInt(paneFields[8]),
				PaneTTY:         paneFields[9],
				ClientTTY:       "",
			},
			ServerIdentity: paneFields[10],
			PanePID:        0,
			PaneTTY:        "",
		}
		pane.PanePID = pane.Tmux.PanePID
		pane.PaneTTY = pane.Tmux.PaneTTY
		panes = append(panes, pane)
	}

	return panes, nil
}

func tmuxServerSocket(tmuxEnv string) string {
	tmuxEnv = strings.TrimSpace(tmuxEnv)
	if tmuxEnv == "" {
		return ""
	}

	socket, _, _ := strings.Cut(tmuxEnv, ",")

	return socket
}

func parseTmuxFields(output string, expectedFields int) ([]string, error) {
	trimmed := strings.TrimRight(output, "\r\n")
	if trimmed == "" {
		return nil, nil
	}

	escapedFields, ok, err := parseEscapedFields(trimmed)
	if err != nil {
		return nil, err
	}
	if ok {
		return escapedFields, nil
	}

	fields := splitTabFields(trimmed, expectedFields)
	if len(fields) != expectedFields {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, expectedFields, len(fields))
	}
	return fields, nil
}

func splitTabFields(output string, expectedFields int) []string {
	fields := strings.Split(output, fieldSeparator)
	if len(fields) > expectedFields && expectedFields > 8 {
		pathParts := len(fields) - expectedFields + 1
		merged := make([]string, 0, expectedFields)
		merged = append(merged, fields[:7]...)
		merged = append(merged, strings.Join(fields[7:7+pathParts], fieldSeparator))
		merged = append(merged, fields[7+pathParts:]...)

		return merged
	}

	return fields
}

func parseEscapedFields(output string) ([]string, bool, error) {
	if !strings.Contains(output, escapedFieldPrefix) {
		return nil, false, nil
	}
	normalized := escapeUnquotedTabs(output)
	words, err := shlex.Split(normalized)
	if err != nil {
		return nil, false, fmt.Errorf("parsing tmux fields: %w", err)
	}
	if len(words) == 0 {
		return nil, false, nil
	}

	fields := make([]string, 0, len(words))
	for _, word := range words {
		if !strings.HasPrefix(word, escapedFieldPrefix) {
			return nil, false, nil
		}
		fields = append(fields, strings.TrimPrefix(word, escapedFieldPrefix))
	}

	return fields, true, nil
}

func escapeUnquotedTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '\\':
			b.WriteByte(c)
			if i+1 < len(s) {
				i++
				c = s[i]
			}
		case c == '\t' && !inSingle && !inDouble:
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
