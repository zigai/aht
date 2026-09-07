package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/shlex"

	"github.com/zigai/aht/internal/command"
	"github.com/zigai/aht/pkg/registry"
)

const (
	fieldSeparator           = "\t"
	escapedFieldPrefix       = "tmuxctx:"
	listPaneFieldCount       = 11
	legacyListPaneFieldCount = 10
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

// CommandRunner executes tmux with an explicit argv. Implementations must not
// invoke a shell.
type CommandRunner func(context.Context, Env, ...string) (string, error)

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
	Run             CommandRunner
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

	format := currentFormat()
	output, err := runTmuxWithEnv(ctx, env, currentDisplayMessageArgs(format, env.TMUXPane)...)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return registry.TmuxContext{}, fmt.Errorf("current tmux context: %w", contextErr)
		}
		if paneID := env.TMUXPane; paneID != "" {
			return ContextFromEnv(env), nil
		}

		return registry.TmuxContext{}, err
	}

	current, err := ParseCurrent(output)
	if err != nil {
		return registry.TmuxContext{}, err
	}
	current.ServerSocket = tmuxServerSocket(env.TMUX)

	return current, nil
}

func ContextFromEnv(env Env) registry.TmuxContext {
	if env.TMUX == "" && env.TMUXPane == "" {
		var tmux registry.TmuxContext

		return tmux
	}

	return registry.TmuxContext{
		Inside:          true,
		ServerSocket:    tmuxServerSocket(env.TMUX),
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
	if options.Run == nil {
		options.Run = runTmuxWithEnv
	}
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
		args := append([]string{}, server.Args...)
		args = append(args, "list-panes", "-a", "-F", listPanesFormat())
		output, runErr := options.Run(ctx, env, args...)
		if runErr != nil {
			if server.Identity == currentServerSocket && firstErr == nil {
				firstErr = runErr
			}
			continue
		}
		serverPanes, parseErr := ParseListPanes(output)
		if parseErr != nil {
			return nil, parseErr
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
	return sendInterrupt(ctx, serverIdentity, paneID, runTmuxWithEnv)
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
	if ok {
		return panesFromFields(fields)
	}

	return parseLegacyListPanes(trimmed)
}

func currentDisplayMessageArgs(format string, paneID string) []string {
	args := []string{"display-message", "-p"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}

	return append(args, "-F", format)
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

func currentFormat() string {
	return tmuxFormat([]string{
		"session_id",
		"session_name",
		"window_id",
		"window_index",
		"window_name",
		"pane_id",
		"pane_index",
		"pane_current_path",
		"pane_pid",
		"pane_tty",
		"client_tty",
	})
}

func listPanesFormat() string {
	return tmuxFormat([]string{
		"session_id",
		"session_name",
		"window_id",
		"window_index",
		"window_name",
		"pane_id",
		"pane_index",
		"pane_current_path",
		"pane_pid",
		"pane_tty",
		"socket_path",
	})
}

func sendInterrupt(ctx context.Context, serverIdentity, paneID string, run CommandRunner) error {
	if strings.TrimSpace(paneID) == "" {
		return errMissingTmuxPaneID
	}
	serverArgs, err := serverArgsForIdentity(serverIdentity)
	if err != nil {
		return err
	}
	args := append([]string{}, serverArgs...)
	args = append(args, "send-keys", "-t", paneID, "C-c")
	_, err = run(ctx, Env{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")}, args...)
	if err != nil {
		return err
	}

	return nil
}

func panesFromFields(fields []string) ([]Pane, error) {
	fieldCount := listPaneFieldCount
	if len(fields)%fieldCount != 0 {
		if len(fields)%legacyListPaneFieldCount != 0 {
			return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, fieldCount, len(fields))
		}
		fieldCount = legacyListPaneFieldCount
	}

	panes := make([]Pane, 0, len(fields)/fieldCount)
	for row := range len(fields) / fieldCount {
		offset := row * fieldCount
		paneFields := fields[offset : offset+fieldCount]
		serverIdentity := ""
		if fieldCount == listPaneFieldCount {
			serverIdentity = paneFields[10]
		}
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
			ServerIdentity: serverIdentity,
			PanePID:        0,
			PaneTTY:        "",
		}
		pane.PanePID = pane.Tmux.PanePID
		pane.PaneTTY = pane.Tmux.PaneTTY
		panes = append(panes, pane)
	}

	return panes, nil
}

func parseLegacyListPanes(output string) ([]Pane, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	fields := make([]string, 0, len(lines)*listPaneFieldCount)
	for _, line := range lines {
		raw := strings.Split(line, fieldSeparator)
		lineFields := raw
		switch {
		case len(raw) == legacyListPaneFieldCount:
			lineFields = append(lineFields, "")
		case len(raw) == listPaneFieldCount && parsePositiveInt(raw[8]) > 0:
			// Current format with socket_path.
		case len(raw) >= listPaneFieldCount && parsePositiveInt(raw[len(raw)-3]) > 0:
			lineFields = splitLegacyFields(line, listPaneFieldCount)
		default:
			lineFields = splitLegacyFields(line, legacyListPaneFieldCount)
			if len(lineFields) == legacyListPaneFieldCount {
				lineFields = append(lineFields, "")
			}
		}
		if len(lineFields) != listPaneFieldCount {
			return nil, fmt.Errorf("%w: expected %d, got %d", ErrInvalidFieldCount, listPaneFieldCount, len(lineFields))
		}
		fields = append(fields, lineFields...)
	}

	return panesFromFields(fields)
}

func tmuxFormat(fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, escapedFieldPrefix+"#{q:"+field+"}")
	}

	return strings.Join(parts, " ")
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

	return splitLegacyFields(trimmed, expectedFields), nil
}

func parseEscapedFields(output string) ([]string, bool, error) {
	if !strings.Contains(output, escapedFieldPrefix) {
		return nil, false, nil
	}
	normalized := escapeUnquotedTabs(normalizeLegacyTmuxDollarEscapes(output))
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

func normalizeLegacyTmuxDollarEscapes(output string) string {
	var normalized strings.Builder
	last := 0
	changed := false
	for index := 0; index < len(output); {
		if output[index] != '\\' {
			index++
			continue
		}
		start := index
		for index < len(output) && output[index] == '\\' {
			index++
		}
		if index >= len(output) || output[index] != '$' || (index-start)%2 != 0 {
			continue
		}
		if !changed {
			normalized.Grow(len(output))
			changed = true
		}
		normalized.WriteString(output[last:start])
		normalized.WriteString(output[start+1 : index])
		normalized.WriteByte('$')
		index++
		last = index
	}
	if !changed {
		return output
	}
	normalized.WriteString(output[last:])
	return normalized.String()
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

func splitLegacyFields(output string, expectedFields int) []string {
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

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func runTmuxWithEnv(ctx context.Context, env Env, args ...string) (string, error) {
	output, err := command.Run(ctx, "tmux", tmuxCommandEnv(env), args...)
	if err != nil {
		return "", fmt.Errorf("running tmux %s: %w", strings.Join(args, " "), err)
	}

	return string(output), nil
}

func tmuxCommandEnv(env Env) []string {
	const tmuxEnvOverrideCount = 2

	values := make([]string, 0, len(os.Environ())+tmuxEnvOverrideCount)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "TMUX=") || strings.HasPrefix(value, "TMUX_PANE=") {
			continue
		}
		values = append(values, value)
	}
	values = append(values, "TMUX="+env.TMUX)
	if env.TMUXPane != "" {
		values = append(values, "TMUX_PANE="+env.TMUXPane)
	}

	return values
}
