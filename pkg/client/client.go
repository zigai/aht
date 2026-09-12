package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

const (
	// ModeAuto routes operations through the realtime broker and falls back to
	// the durable registry on disk when the broker is offline.
	// This is the default mode.
	ModeAuto Mode = "auto"

	// ModeRealtimeOnly directs all operations strictly to the realtime broker socket.
	// When the broker is offline, operations fail immediately with [ErrUnavailable]
	// without reading disk or taking filesystem locks.
	ModeRealtimeOnly Mode = "realtime"

	// ModeDurableOnly directs all operations directly to the on-disk registry file,
	// bypassing the realtime broker entirely.
	ModeDurableOnly Mode = "durable"

	PresenceLive    Presence = registry.PresenceLive
	PresenceGone    Presence = registry.PresenceGone
	PresenceUnknown Presence = registry.PresenceUnknown

	ActivityRunning     Activity = registry.ActivityRunning
	ActivityWaiting     Activity = registry.ActivityWaiting
	ActivityIdle        Activity = registry.ActivityIdle
	ActivityFailed      Activity = registry.ActivityFailed
	ActivityInterrupted Activity = registry.ActivityInterrupted
	ActivityUnknown     Activity = registry.ActivityUnknown

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
	// ErrUnavailable means no realtime AHT broker accepted the local connection.
	ErrUnavailable = errors.New("aht broker unavailable")
	// ErrProtocol means the broker returned an invalid or incompatible response.
	ErrProtocol         = errors.New("aht broker protocol error")
	errHandlerRequired  = errors.New("watch handler is required")
	ErrRealtimeRequired = errors.New("operation requires a realtime broker connection")
	// ErrInvalidMode means a client was configured with an unsupported Mode.
	ErrInvalidMode = errors.New("invalid aht client mode")

	_ registry.Store = (*Client)(nil)
)

// Mode controls how a Client routes operations between the realtime broker
// and the durable registry file on disk.
type Mode string

type (
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

	// Summary represents aggregate session counts for a terminal session.
	Summary = registry.Summary

	// Subscription streams independently owned snapshots from the realtime broker.
	Subscription = broker.Subscription
)

// Config identifies the local AHT instance used by a Client. Empty fields use
// the current user's default registry and its associated broker socket.
type Config struct {
	StorePath  string
	SocketPath string
	Mode       Mode
}

// stateStore is the routing client's operational contract.
type stateStore interface {
	Observe(ctx context.Context, observation registry.Observation) (registry.Session, error)
	ObserveBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error)
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
	Get(ctx context.Context, id string) (registry.Session, error)
	SummaryByTmuxSession(ctx context.Context, filter registry.Filter) ([]registry.Summary, error)
	GC(ctx context.Context, maxAge time.Duration) (registry.GCResult, error)
}

// Client reads and updates agent-harness state through the local AHT broker.
// Depending on [Mode], operations route to the realtime broker socket, durable
// disk storage, or auto-fallback between the two.
type Client struct {
	mode      Mode
	storePath string
	store     stateStore
	realtime  *broker.Client
	configErr error
}

// OperationError is a machine-readable failure returned by the AHT broker.
type OperationError struct {
	Code    string
	Message string
}

// New returns a client for the configured local AHT instance. An unsupported
// Mode makes all operations return ErrInvalidMode without performing I/O.
func New(config Config) *Client {
	mode := config.Mode
	if mode == "" {
		mode = ModeAuto
	}

	storePath := config.StorePath
	if storePath == "" {
		storePath = registry.DefaultStorePath()
	}

	socketPath := config.SocketPath
	if socketPath == "" {
		socketPath = broker.SocketPath(storePath)
	}

	realtime := broker.NewClientForSocket(socketPath)

	var store stateStore
	var configErr error
	switch mode {
	case ModeRealtimeOnly:
		store = realtime
	case ModeDurableOnly:
		store = registry.NewFileStore(storePath)
	case ModeAuto:
		store = broker.NewStoreForSocket(storePath, socketPath)
	default:
		configErr = fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}

	return &Client{
		mode:      mode,
		storePath: storePath,
		store:     store,
		realtime:  realtime,
		configErr: configErr,
	}
}

// StorePath returns the durable registry path used for broker fallback.
func (c *Client) StorePath() string {
	return c.storePath
}

// SocketPath returns the broker endpoint used by the client.
func (c *Client) SocketPath() string {
	return c.realtime.SocketPath()
}

// Mode returns the configured operating mode.
func (c *Client) Mode() Mode {
	return c.mode
}

// Realtime returns the underlying realtime broker socket client.
func (c *Client) Realtime() *broker.Client {
	return c.realtime
}

// Subscribe returns an active subscription streaming state snapshots from the broker.
// Subscribe requires a running broker and is not supported in [ModeDurableOnly].
func (c *Client) Subscribe(ctx context.Context, filter registry.Filter) (*broker.Subscription, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	if c.mode == ModeDurableOnly {
		return nil, ErrRealtimeRequired
	}

	subscription, err := c.realtime.Subscribe(ctx, filter)
	if err != nil {
		return nil, publicError(err)
	}

	return subscription, nil
}

// Ping verifies that the realtime broker is accepting requests.
func (c *Client) Ping(ctx context.Context) error {
	if c.configErr != nil {
		return c.configErr
	}
	return publicError(c.realtime.Ping(ctx))
}

// Observe records one agent-harness observation using the configured Mode.
// Only ModeAuto falls back to durable storage when the broker is unavailable.
func (c *Client) Observe(ctx context.Context, observation registry.Observation) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}
	session, err := c.store.Observe(ctx, observation)
	return session, publicError(err)
}

// ObserveBatch atomically records a group of agent-harness observations.
func (c *Client) ObserveBatch(ctx context.Context, observations []registry.Observation) ([]registry.Session, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	sessions, err := c.store.ObserveBatch(ctx, observations)
	return sessions, publicError(err)
}

// List returns all sessions matching filter.
func (c *Client) List(ctx context.Context, filter registry.Filter) ([]registry.Session, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	sessions, err := c.store.List(ctx, filter)
	return sessions, publicError(err)
}

// Get returns the session identified by id.
func (c *Client) Get(ctx context.Context, id string) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}
	session, err := c.store.Get(ctx, id)
	return session, publicError(err)
}

// Summary returns aggregate session counts grouped by terminal-multiplexer session.
func (c *Client) Summary(ctx context.Context, filter registry.Filter) ([]registry.Summary, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}
	summaries, err := c.store.SummaryByTmuxSession(ctx, filter)
	return summaries, publicError(err)
}

// SummaryByTmuxSession implements registry.Store.
func (c *Client) SummaryByTmuxSession(ctx context.Context, filter registry.Filter) ([]registry.Summary, error) {
	return c.Summary(ctx, filter)
}

// GC removes gone-session tombstones at least deleteAfter old.
func (c *Client) GC(ctx context.Context, deleteAfter time.Duration) (registry.GCResult, error) {
	if c.configErr != nil {
		return registry.GCResult{}, c.configErr
	}
	result, err := c.store.GC(ctx, deleteAfter)
	return result, publicError(err)
}

// Watch calls yield with the initial filtered snapshot and each strictly newer
// revision until ctx is canceled. Watch returns nil after cancellation.
func (c *Client) Watch(
	ctx context.Context,
	filter registry.Filter,
	yield func(registry.StateSnapshot) error,
) error {
	if c.configErr != nil {
		return c.configErr
	}
	if yield == nil {
		return errHandlerRequired
	}

	subscription, err := c.Subscribe(ctx, filter)
	if err != nil {
		return err
	}
	defer subscription.Close()
	return watchSnapshots(ctx, subscription, yield)
}

// IsUnavailable reports whether err means that no realtime broker accepted the connection.
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable)
}

func watchSnapshots(ctx context.Context, subscription *broker.Subscription, yield func(registry.StateSnapshot) error) error {
	snapshots := subscription.Snapshots
	errorsChannel := subscription.Errors
	for snapshots != nil || errorsChannel != nil {
		select {
		case <-ctx.Done():
			return nil
		case snapshot, ok := <-snapshots:
			if !ok {
				snapshots = nil
				continue
			}
			if err := yield(snapshot); err != nil {
				return fmt.Errorf("handling AHT state snapshot: %w", err)
			}
		case watchErr, ok := <-errorsChannel:
			if !ok {
				errorsChannel = nil
				continue
			}
			if watchErr != nil {
				return publicError(watchErr)
			}
		}
	}

	return nil
}

func (e *OperationError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func publicError(err error) error {
	if err == nil {
		return nil
	}
	if broker.IsUnavailable(err) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if errors.Is(err, broker.ErrProtocol) {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}

	if remoteError, ok := errors.AsType[*broker.RemoteError](err); ok {
		return &OperationError{Code: remoteError.Code, Message: remoteError.Message}
	}
	return err
}
