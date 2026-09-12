package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/zigai/aht/internal/cancelclose"
	"github.com/zigai/aht/pkg/registry"
)

const (
	defaultDialTimeout = 100 * time.Millisecond
	// MaxResponseBytes bounds each newline-delimited broker response, including its newline.
	MaxResponseBytes = 64 << 20
)

var (
	nextRequestID atomic.Uint64
	_             registry.Store = (*Client)(nil)
)

// Client sends registry operations to one local broker.
type Client struct {
	socketPath  string
	dialTimeout time.Duration
}

// Subscription streams independently owned effective-state snapshots until its
// context is canceled. Callers may modify a received snapshot without affecting
// the broker or subsequent snapshots.
type Subscription struct {
	Snapshots <-chan registry.StateSnapshot
	Errors    <-chan error
	cancel    context.CancelFunc
	done      <-chan struct{}
}

// RemoteError is an operation error returned by the broker.
type RemoteError struct {
	Code    string
	Message string
}

// NewClient returns a client for the broker associated with storePath.
func NewClient(storePath string) *Client {
	return NewClientForSocket(SocketPath(storePath))
}

func NewClientForSocket(socketPath string) *Client {
	return &Client{socketPath: socketPath, dialTimeout: defaultDialTimeout}
}

// SocketPath returns the exact endpoint used by the client.
func (c *Client) SocketPath() string { return c.socketPath }

// Ping verifies that the broker is accepting requests.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.roundTrip(ctx, newRequest(MethodPing))

	return err
}

// Observe records one observation in the broker.
func (c *Client) Observe(ctx context.Context, observation registry.Observation) (registry.Session, error) {
	request := newRequest(MethodObserve)
	request.Observation = &observation
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return registry.Session{}, err
	}
	if response.Session == nil {
		return registry.Session{}, fmt.Errorf("%w: observe response omitted session", ErrProtocol)
	}

	return *response.Session, nil
}

// ObserveBatch records a group of observations atomically in the broker.
func (c *Client) ObserveBatch(
	ctx context.Context,
	observations []registry.Observation,
) ([]registry.Session, error) {
	request := newRequest(MethodObserveBatch)
	request.Observations = observations
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return nil, err
	}

	return response.Sessions, nil
}

// List returns the broker's current filtered sessions.
func (c *Client) List(ctx context.Context, filter registry.Filter) ([]registry.Session, error) {
	request := newRequest(MethodList)
	request.Filter = filter
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return nil, err
	}

	return response.Sessions, nil
}

// Get returns one broker-owned session.
func (c *Client) Get(ctx context.Context, id string) (registry.Session, error) {
	request := newRequest(MethodGet)
	request.SessionID = id
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return registry.Session{}, err
	}
	if response.Session == nil {
		return registry.Session{}, fmt.Errorf("%w: get response omitted session", ErrProtocol)
	}

	return *response.Session, nil
}

// Summary returns filtered multiplexer-session summaries.
func (c *Client) Summary(ctx context.Context, filter registry.Filter) ([]registry.Summary, error) {
	request := newRequest(MethodSummary)
	request.Filter = filter
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return nil, err
	}

	return response.Summaries, nil
}

// SummaryByTmuxSession implements registry.Store.
func (c *Client) SummaryByTmuxSession(ctx context.Context, filter registry.Filter) ([]registry.Summary, error) {
	return c.Summary(ctx, filter)
}

// GC removes expired gone-session tombstones through the broker.
func (c *Client) GC(ctx context.Context, deleteAfter time.Duration) (registry.GCResult, error) {
	request := newRequest(MethodGC)
	request.DeleteAfter = deleteAfter
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		return registry.GCResult{}, err
	}
	if response.GC == nil {
		return registry.GCResult{}, fmt.Errorf("%w: gc response omitted result", ErrProtocol)
	}

	return *response.GC, nil
}

// NewSubscription returns a Subscription wrapping the given channels and optional cancel function.
func NewSubscription(snapshots <-chan registry.StateSnapshot, errors <-chan error, cancel context.CancelFunc) *Subscription {
	return &Subscription{Snapshots: snapshots, Errors: errors, cancel: cancel, done: nil}
}

// Close cancels the subscription and waits for its broker reader to finish.
// For subscriptions created with NewSubscription, the supplied cancel function
// owns any external worker cleanup. It is safe to call Close more than once.
func (s *Subscription) Close() {
	if s != nil && s.cancel != nil {
		s.cancel()
	}
	if s != nil && s.done != nil {
		<-s.done
	}
}

// Subscribe returns the initial snapshot followed by strictly newer revisions.
func (c *Client) Subscribe(ctx context.Context, filter registry.Filter) (*Subscription, error) {
	subscriptionContext, cancel := context.WithCancel(ctx)
	connection, err := c.dial(subscriptionContext)
	if err != nil {
		cancel()
		return nil, err
	}
	cleanup := cancelclose.OnCancel(subscriptionContext, connection)
	transferred := false
	defer func() {
		if !transferred {
			cancel()
			cleanup()
		}
	}()

	request := newRequest(MethodSubscribe)
	request.Filter = filter
	request = request.prepare()
	//nolint:musttag // Request defines the complete public JSON protocol schema.
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, fmt.Errorf("sending subscribe request: %w", contextError(subscriptionContext, err))
	}

	decoder := responseScanner(connection)
	first, err := readResponse(decoder)
	if err != nil {
		return nil, fmt.Errorf("reading subscribe response: %w", contextError(subscriptionContext, err))
	}
	if err := validateResponse(request, first); err != nil {
		return nil, err
	}
	if first.Snapshot == nil {
		return nil, fmt.Errorf("%w: subscribe response omitted snapshot", ErrProtocol)
	}

	snapshots := make(chan registry.StateSnapshot, 1)
	errorsChannel := make(chan error, 1)
	snapshots <- *first.Snapshot

	done := make(chan struct{})
	transferred = true
	go func() {
		defer close(done)
		defer cancel()
		defer cleanup()
		runSubscription(subscriptionContext, decoder, request, snapshots, errorsChannel)
	}()

	return &Subscription{Snapshots: snapshots, Errors: errorsChannel, cancel: cancel, done: done}, nil
}

// IsUnavailable reports whether err means no broker accepted the connection.
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }

func runSubscription(
	ctx context.Context,
	decoder *bufio.Scanner,
	request Request,
	snapshots chan<- registry.StateSnapshot,
	errorsChannel chan<- error,
) {
	defer close(snapshots)
	defer close(errorsChannel)

	for {
		response, err := readResponse(decoder)
		if err != nil {
			if ctx.Err() == nil {
				errorsChannel <- fmt.Errorf("reading subscription: %w", err)
			}
			return
		}
		if err := validateResponse(request, response); err != nil {
			errorsChannel <- err
			return
		}
		if response.Type == "heartbeat" || response.Snapshot == nil {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case snapshots <- *response.Snapshot:
		}
	}
}

func (c *Client) roundTrip(ctx context.Context, request Request) (Response, error) {
	connection, err := c.dial(ctx)
	if err != nil {
		return Response{}, err
	}
	defer cancelclose.OnCancel(ctx, connection)()

	request = request.prepare()
	//nolint:musttag // Request defines the complete public JSON protocol schema.
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, fmt.Errorf("sending broker request: %w", contextError(ctx, err))
	}

	response, err := readResponse(responseScanner(connection))
	if err != nil {
		return Response{}, fmt.Errorf("reading broker response: %w", contextError(ctx, err))
	}
	if err := validateResponse(request, response); err != nil {
		return Response{}, err
	}

	return response, nil
}

func responseScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, MaxResponseBytes)
	return scanner
}

func readResponse(scanner *bufio.Scanner) (Response, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Response{}, fmt.Errorf("%w: reading response frame: %w", ErrProtocol, err)
		}
		return Response{}, io.EOF
	}
	var response Response
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return Response{}, fmt.Errorf("%w: decoding response frame: %w", ErrProtocol, err)
	}
	return response, nil
}

func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("broker operation canceled: %w", ctx.Err())
	}
	return err
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	dialContext := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok && c.dialTimeout > 0 {
		dialContext, cancel = context.WithTimeout(ctx, c.dialTimeout)
	}
	defer cancel()

	connection, err := new(net.Dialer).DialContext(dialContext, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w at %s: %w", ErrUnavailable, c.socketPath, err)
	}

	return connection, nil
}

func newRequest(method string) Request {
	return Request{
		Version:      0,
		ID:           "",
		Method:       method,
		Observation:  nil,
		Observations: nil,
		Filter: registry.Filter{
			Harness:            "",
			Presence:           "",
			Activity:           "",
			TmuxSession:        "",
			MultiplexerSession: "",
		},
		SessionID:   "",
		DeleteAfter: 0,
	}
}

func (request Request) prepare() Request {
	request.Version = ProtocolVersion
	if request.ID == "" {
		request.ID = strconv.FormatUint(nextRequestID.Add(1), 10)
	}

	return request
}

func validateResponse(request Request, response Response) error {
	if response.Version != ProtocolVersion {
		return fmt.Errorf("%w: version %d", ErrProtocol, response.Version)
	}
	if response.ID != request.ID {
		return fmt.Errorf("%w: response id %q does not match %q", ErrProtocol, response.ID, request.ID)
	}
	if response.Error != nil {
		return &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}

	return nil
}

func (e *RemoteError) Error() string {
	if e.Message == "" {
		return e.Code
	}

	return e.Code + ": " + e.Message
}

// Unwrap classifies recognized registry failures without changing the wire error.
func (e *RemoteError) Unwrap() error {
	switch e.Code {
	case "not_found":
		return registry.ErrSessionNotFound
	case "observation_conflict":
		return registry.ErrObservationConflict
	default:
		return nil
	}
}
