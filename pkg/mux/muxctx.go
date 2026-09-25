package mux

import (
	"context"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

// Driver represents a terminal multiplexer capable of discovering and capturing panes.
//
// Behavioral contract:
//   - Kind returns the canonical multiplexer identifier.
//   - Current returns the enclosing multiplexer context for the caller.
//     If the caller is not running inside this multiplexer, it returns an empty
//     [registry.Location] and nil error. A non-nil error indicates an
//     operational inspection failure or context cancellation.
//   - ListPanes enumerates live panes for this multiplexer.
//     If the multiplexer binary is not installed or no active server/session exists,
//     it returns nil, nil. A non-nil error indicates an operational failure.
//   - CapturePane captures the visible terminal screen and title of the given pane.
type Driver interface {
	Kind() registry.MultiplexerKind
	Current(ctx context.Context) (registry.Location, error)
	ListPanes(ctx context.Context) ([]Pane, error)
	CapturePane(ctx context.Context, pane Pane) (ScreenSnapshot, error)
}

// Interrupter is an optional capability interface for multiplexers that
// support sending interrupt signals directly to a multiplexer pane.
type Interrupter interface {
	SendInterrupt(ctx context.Context, serverIdentity, paneID string) error
}

// ProcessRef identifies a process reported by a multiplexer for one pane.
// Start identity is resolved from the observer's own process snapshot.
type ProcessRef struct {
	PID            int
	ProcessGroupID int
	Command        string
	CWD            string
}

// Pane is transient native multiplexer inventory. Only Location is persisted.
type Pane struct {
	Location    registry.Location
	Processes   []ProcessRef
	ProcessTTY  string
	Command     string
	CWD         string
	Title       string
	Activity    *registry.Activity
	StateReason string
}

type ScreenSnapshot struct {
	Text  string
	Title string
}

type (
	PaneLister     func(context.Context) ([]Pane, error)
	ScreenCapturer func(context.Context, Pane) (ScreenSnapshot, error)
)

// BoundBottomLines normalizes line endings, strips any trailing empty line, and returns at most limit bottom lines.
func BoundBottomLines(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

// NormalizePaneID normalizes pane identifiers across multiplexers.
// For Zellij, bare numeric IDs gain a "terminal_" prefix.
func NormalizePaneID(kind registry.MultiplexerKind, paneID string) string {
	paneID = strings.TrimSpace(paneID)
	if kind == registry.MultiplexerZellij && paneID != "" && !strings.HasPrefix(paneID, "terminal_") && !strings.HasPrefix(paneID, "plugin_") {
		return "terminal_" + paneID
	}
	return paneID
}
