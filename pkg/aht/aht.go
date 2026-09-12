package aht

import (
	"context"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

const (
	// ModeAuto routes through the broker socket and falls back to disk if offline.
	ModeAuto Mode = client.ModeAuto

	// ModeRealtimeOnly connects strictly to the broker socket, failing if offline.
	ModeRealtimeOnly Mode = client.ModeRealtimeOnly

	// ModeDurableOnly reads directly from the on-disk registry file.
	ModeDurableOnly Mode = client.ModeDurableOnly

	// PresenceLive indicates the agent session process or container is active.
	PresenceLive Presence = registry.PresenceLive

	// PresenceGone indicates the agent session has terminated.
	PresenceGone Presence = registry.PresenceGone

	// PresenceUnknown indicates presence cannot be determined.
	PresenceUnknown Presence = registry.PresenceUnknown

	// ActivityRunning indicates the agent is actively processing or executing.
	ActivityRunning Activity = registry.ActivityRunning

	// ActivityWaiting indicates the agent is waiting for user or tool input.
	ActivityWaiting Activity = registry.ActivityWaiting

	// ActivityIdle indicates the agent is idle and ready for interaction.
	ActivityIdle Activity = registry.ActivityIdle

	// ActivityFailed indicates the agent encountered an unrecoverable failure.
	ActivityFailed Activity = registry.ActivityFailed

	// ActivityInterrupted indicates the agent run was canceled or interrupted.
	ActivityInterrupted Activity = registry.ActivityInterrupted

	// ActivityUnknown indicates activity cannot be determined.
	ActivityUnknown Activity = registry.ActivityUnknown

	HarnessClaude   Harness = registry.HarnessClaude
	HarnessCodex    Harness = registry.HarnessCodex
	HarnessCursor   Harness = registry.HarnessCursor
	HarnessCopilot  Harness = registry.HarnessCopilot
	HarnessCline    Harness = registry.HarnessCline
	HarnessKimiCode Harness = registry.HarnessKimiCode
	HarnessGrok     Harness = registry.HarnessGrok
	HarnessGoose    Harness = registry.HarnessGoose
	HarnessPi       Harness = registry.HarnessPi
	HarnessOmp      Harness = registry.HarnessOmp
	HarnessOpenCode Harness = registry.HarnessOpenCode
	HarnessAgy      Harness = registry.HarnessAgy
	HarnessKilo     Harness = registry.HarnessKilo
	HarnessDroid    Harness = registry.HarnessDroid
	HarnessOpenClaw Harness = registry.HarnessOpenClaw
	HarnessHermes   Harness = registry.HarnessHermes
)

var (
	// ErrInvalidMode means the client was configured with an unsupported operating mode.
	ErrInvalidMode = client.ErrInvalidMode

	// ErrUnavailable means no realtime AHT broker accepted the local connection.
	ErrUnavailable = client.ErrUnavailable

	// ErrProtocol means the broker returned an invalid or incompatible response.
	ErrProtocol = client.ErrProtocol

	// ErrRealtimeRequired means the operation requires a realtime broker connection.
	ErrRealtimeRequired = client.ErrRealtimeRequired
)

type (
	// Client reads and updates agent-harness state through the local AHT broker.
	Client = client.Client

	// Config identifies the local AHT instance and operating mode used by a Client.
	Config = client.Config

	// Mode controls how a Client routes operations between the realtime broker
	// and the durable registry file on disk.
	Mode = client.Mode

	// Session represents an agent-harness session tracked by AHT.
	Session = registry.Session

	// Filter specifies matching criteria when querying or watching sessions.
	Filter = registry.Filter

	// StateSnapshot is a revisioned collection of tracked sessions.
	StateSnapshot = registry.StateSnapshot

	// Presence indicates whether an agent session is live, gone, or unknown.
	Presence = registry.Presence

	// Activity indicates what an agent is currently doing.
	Activity = registry.Activity

	// Harness identifies a supported AI coding agent.
	Harness = registry.Harness

	// TmuxContext represents the tmux multiplexer location of a session.
	TmuxContext = registry.TmuxContext

	// MultiplexerContext represents the unified multiplexer location of a session.
	MultiplexerContext = registry.MultiplexerContext

	// Observation represents an observation recorded for a session.
	Observation = registry.Observation

	// ObservationIdentity identifies the session an observation belongs to.
	ObservationIdentity = registry.ObservationIdentity

	// Summary represents aggregate session counts for a terminal session.
	Summary = registry.Summary

	// Subscription streams immutable state snapshots from the realtime broker.
	Subscription = broker.Subscription

	// Store represents an engine that persists or serves agent-harness state.
	Store = registry.Store
)

// New returns a client for the configured local AHT instance.
func New(config Config) *Client {
	return client.New(config)
}

// DefaultSocketPath returns the default endpoint for the current user's AHT broker.
func DefaultSocketPath() string {
	return broker.DefaultSocketPath()
}

// DefaultStorePath returns the default filesystem location for the durable registry.
func DefaultStorePath() string {
	return registry.DefaultStorePath()
}

// NewSubscription creates a subscription wrapping custom channels.
func NewSubscription(snapshots <-chan StateSnapshot, errors <-chan error, cancel context.CancelFunc) *Subscription {
	return broker.NewSubscription(snapshots, errors, cancel)
}

// IsUnavailable reports whether err means that no realtime broker accepted the connection.
func IsUnavailable(err error) bool {
	return client.IsUnavailable(err)
}
