package broker

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// ProtocolVersion is the current newline-delimited JSON protocol version.
	ProtocolVersion = 2

	MethodPing         = "ping"
	MethodObserve      = "observe"
	MethodObserveBatch = "observe_batch"
	MethodList         = "list"
	MethodGet          = "get"
	MethodSummary      = "summary"
	MethodGC           = "gc"
	MethodSubscribe    = "subscribe"
	MethodReset        = "reset"
)

// SocketPathEnv overrides the broker socket used by clients and integrations.
const SocketPathEnv = "AHT_SOCKET"

var (
	// ErrUnavailable means no realtime broker accepted the local connection.
	ErrUnavailable = errors.New("aht broker unavailable")
	// ErrProtocol means the broker returned an invalid or incompatible frame.
	ErrProtocol = errors.New("aht broker protocol error")
)

// Request is one broker RPC. Subscribe keeps the connection open after the
// initial response and streams Snapshot frames.
type Request struct {
	Version        int                     `json:"version"`
	ID             string                  `json:"id,omitempty"`
	Method         string                  `json:"method"`
	Observation    *registry.Observation   `json:"observation,omitempty"`
	Observations   []registry.Observation  `json:"observations,omitempty"`
	Filter         registry.Filter         `json:"filter,omitzero"`
	SessionID      string                  `json:"session_id,omitempty"`
	DeleteAfter    time.Duration           `json:"delete_after,omitempty"`
	SummaryOptions registry.SummaryOptions `json:"summary_options,omitzero"`
}

// Response is one broker result or subscription frame.
type Response struct {
	Version   int                     `json:"version"`
	ID        string                  `json:"id,omitempty"`
	Type      string                  `json:"type"`
	Error     *Error                  `json:"error,omitempty"`
	Session   *registry.Session       `json:"session,omitempty"`
	Sessions  []registry.Session      `json:"sessions,omitempty"`
	Summaries []registry.Summary      `json:"summaries,omitempty"`
	Snapshot  *registry.StateSnapshot `json:"snapshot,omitempty"`
	GC        *registry.GCResult      `json:"gc,omitempty"`
	Reset     *registry.ResetResult   `json:"reset,omitempty"`
	Now       time.Time               `json:"now,omitzero"`
}

// Error is a machine-readable broker failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SocketPath returns the local socket for one registry snapshot path.
func SocketPath(storePath string) string {
	if override := os.Getenv(SocketPathEnv); override != "" {
		return override
	}
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}

	return filepath.Clean(storePath) + ".sock"
}

// DefaultSocketPath returns the default endpoint for the current user's AHT broker.
func DefaultSocketPath() string {
	return SocketPath("")
}
