package mux

import (
	"context"
	"strings"

	"github.com/zigai/aht/v2/internal/processinfo"
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
//   - EnvKeys lists the environment variables LocationFromEnv reads.
//   - LocationFromEnv returns the pane a process environment places the process
//     in, or an empty location when the environment does not name one.
//   - ListPanes enumerates live panes for this multiplexer. processes is the
//     caller's current process snapshot, or nil to let the driver list them.
//     If the multiplexer binary is not installed or no active server/session exists,
//     it returns nil, nil. A non-nil error indicates an operational failure.
//   - CapturePane captures the visible terminal screen and title of the given pane.
//   - Presence reports whether location is still a live pane. Drivers that
//     cannot check a single pane return [registry.PresenceUnknown].
//   - SamePane reports whether two locations of this multiplexer name the same
//     pane, using the identifiers that make a pane id unique.
type Driver interface {
	Kind() registry.MultiplexerKind
	Current(ctx context.Context) (registry.Location, error)
	EnvKeys() []string
	LocationFromEnv(env func(string) string) registry.Location
	ListPanes(ctx context.Context, processes []processinfo.Process) ([]Pane, error)
	CapturePane(ctx context.Context, pane Pane) (ScreenSnapshot, error)
	Presence(ctx context.Context, location registry.Location) (registry.Presence, error)
	SamePane(a registry.Location, b registry.Location) bool
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
	Location registry.Location
	//exhaustruct:optional
	Processes []ProcessRef
	//exhaustruct:optional
	ProcessTTY string
	Command    string
	CWD        string
	Title      string
	//exhaustruct:optional
	Activity *registry.Activity
	//exhaustruct:optional
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
