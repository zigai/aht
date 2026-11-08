package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// ModeAuto routes operations through the realtime broker and falls back to
	// the durable registry on disk when the broker is offline.
	// This is the default mode.
	ModeAuto Mode = "auto"

	// ModeRealtimeOnly directs all operations strictly to the realtime broker socket.
	// When the broker is offline, operations fail immediately with
	// [broker.ErrUnavailable] without reading disk or taking filesystem locks.
	ModeRealtimeOnly Mode = "realtime"

	// ModeDurableOnly directs all operations directly to the on-disk registry file,
	// bypassing the realtime broker entirely.
	ModeDurableOnly Mode = "durable"
)

var (
	// ErrRealtimeRequired means the operation requires a realtime broker connection.
	ErrRealtimeRequired = errors.New("operation requires a realtime broker connection")
	// ErrInvalidMode means a client was configured with an unsupported Mode.
	ErrInvalidMode = errors.New("invalid aht client mode")

	_ registry.Store             = (*Client)(nil)
	_ registry.GroupedSummarizer = (*Client)(nil)
)

// Mode controls how a Client routes operations between the realtime broker
// and the durable registry file on disk.
type Mode string

// Config identifies the local AHT instance used by a Client. Empty fields use
// the current user's default registry and its associated broker socket.
//
//exhaustruct:optional
type Config struct {
	StorePath  string
	SocketPath string
	Mode       Mode
}

type stateStore interface {
	Observe(ctx context.Context, observation registry.Observation) (registry.Session, error)
	ObserveBatch(
		ctx context.Context,
		observations []registry.Observation,
	) ([]registry.Session, error)
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
	Get(ctx context.Context, id string) (registry.Session, error)
	SummaryWithOptions(
		ctx context.Context,
		filter registry.Filter,
		opts registry.SummaryOptions,
	) ([]registry.Summary, error)
	GC(ctx context.Context, maxAge time.Duration) (registry.GCResult, error)
	Reset(ctx context.Context) (registry.ResetResult, error)
}

// Client reads and updates agent-harness state through the local AHT broker.
// Depending on [Mode], operations route to the realtime broker socket, durable
// disk storage, or auto-fallback between the two.
type Client struct {
	mode      Mode
	storePath string
	store     stateStore
	realtime  realtimeClient
	configErr error
}

type realtimeClient interface {
	Ping(ctx context.Context) error
	SocketPath() string
	Subscribe(ctx context.Context, filter registry.Filter) (*broker.Subscription, error)
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

	realtime := broker.NewClient(socketPath)

	var (
		store     stateStore
		configErr error
	)

	switch mode {
	case ModeRealtimeOnly:
		store = realtime
	case ModeDurableOnly:
		store = registry.NewJournal(storePath, catalog.Rules{})
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

// Subscribe returns an active subscription streaming state snapshots from the broker.
// Subscribe requires a running broker and is not supported in [ModeDurableOnly].
func (c *Client) Subscribe(
	ctx context.Context,
	filter registry.Filter,
) (*broker.Subscription, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}

	if c.mode == ModeDurableOnly {
		return nil, ErrRealtimeRequired
	}

	subscription, err := c.realtime.Subscribe(ctx, filter)
	if err != nil {
		return subscription, fmt.Errorf("subscribing to sessions: %w", err)
	}

	return subscription, nil
}

// Ping verifies that the realtime broker is accepting requests.
func (c *Client) Ping(ctx context.Context) error {
	if c.configErr != nil {
		return c.configErr
	}

	if err := c.realtime.Ping(ctx); err != nil {
		return fmt.Errorf("pinging broker: %w", err)
	}

	return nil
}

// Observe records one agent-harness observation using the configured Mode.
// Only ModeAuto falls back to durable storage when the broker is unavailable.
func (c *Client) Observe(
	ctx context.Context,
	observation registry.Observation,
) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}

	session, err := c.store.Observe(ctx, observation)
	if err != nil {
		return session, fmt.Errorf("recording observation: %w", err)
	}

	return session, nil
}

// ObserveBatch atomically records a group of agent-harness observations.
func (c *Client) ObserveBatch(
	ctx context.Context,
	observations []registry.Observation,
) ([]registry.Session, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}

	sessions, err := c.store.ObserveBatch(ctx, observations)
	if err != nil {
		return sessions, fmt.Errorf("recording observations: %w", err)
	}

	return sessions, nil
}

// List returns all sessions matching filter.
func (c *Client) List(ctx context.Context, filter registry.Filter) ([]registry.Session, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}

	sessions, err := c.store.List(ctx, filter)
	if err != nil {
		return sessions, fmt.Errorf("listing sessions: %w", err)
	}

	return sessions, nil
}

// Get returns the session identified by id.
func (c *Client) Get(ctx context.Context, id string) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}

	session, err := c.store.Get(ctx, id)
	if err != nil {
		return session, fmt.Errorf("getting session: %w", err)
	}

	return session, nil
}

// SummaryWithOptions returns aggregate session counts with the given options.
func (c *Client) SummaryWithOptions(
	ctx context.Context,
	filter registry.Filter,
	opts registry.SummaryOptions,
) ([]registry.Summary, error) {
	if c.configErr != nil {
		return nil, c.configErr
	}

	summaries, err := c.store.SummaryWithOptions(ctx, filter, opts)
	if err != nil {
		return summaries, fmt.Errorf("summarizing sessions: %w", err)
	}

	return summaries, nil
}

// Summary returns aggregate session counts grouped by terminal-multiplexer session.
func (c *Client) Summary(ctx context.Context, filter registry.Filter) ([]registry.Summary, error) {
	return c.SummaryWithOptions(ctx, filter, registry.SummaryOptions{GroupBy: ""})
}

// GC removes gone-session tombstones at least deleteAfter old.
func (c *Client) GC(ctx context.Context, deleteAfter time.Duration) (registry.GCResult, error) {
	if c.configErr != nil {
		return registry.GCResult{}, c.configErr
	}

	result, err := c.store.GC(ctx, deleteAfter)
	if err != nil {
		return result, fmt.Errorf("collecting gone sessions: %w", err)
	}

	return result, nil
}

// Reset deletes every stored session.
func (c *Client) Reset(ctx context.Context) (registry.ResetResult, error) {
	if c.configErr != nil {
		return registry.ResetResult{}, c.configErr
	}

	result, err := c.store.Reset(ctx)
	if err != nil {
		return result, fmt.Errorf("resetting sessions: %w", err)
	}

	return result, nil
}
