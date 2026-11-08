package brokerserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/zigai/aht/v2/internal/cancelclose"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	maxRequestBytes       = 16 << 20
	maxBatchObservations  = 4096
	defaultMaxConnections = 256
	scannerInitialBytes   = 4 << 10
	handshakeTimeout      = 5 * time.Second
	writeTimeout          = 2 * time.Second
	heartbeatInterval     = 15 * time.Second
)

var (
	ErrAlreadyRunning = errors.New("aht broker is already running")

	errContextNil          = errors.New("broker context is nil")
	errStoreNil            = errors.New("broker store is nil")
	errSocketPathEmpty     = errors.New("broker socket path is empty")
	errProtocolVersion     = errors.New("unsupported broker protocol version")
	errRequestIDRequired   = errors.New("broker request id is required")
	errUnknownMethod       = errors.New("unknown broker method")
	errObservationRequired = errors.New("observe requires observation")
	errBatchRequired       = errors.New("observe_batch requires observations")
	errBatchTooLarge       = errors.New("observe_batch has too many observations")
	errSessionIDRequired   = errors.New("get requires session_id")
	errPathNotSocket       = errors.New("broker path is not a socket")
	errResponseTooLarge    = errors.New("broker response exceeds frame limit")
)

// Server owns the local socket API for one in-memory registry.
type Server struct {
	store            *registry.MemoryStore
	socketPath       string
	maxConnections   int
	writeTimeout     time.Duration
	ready            chan<- struct{}
	onConnectionDone func()
}

// Options configures a broker server.
//
//exhaustruct:optional
type Options struct {
	Store            *registry.MemoryStore
	SocketPath       string
	MaxConnections   int
	WriteTimeout     time.Duration
	Ready            chan<- struct{}
	OnConnectionDone func()
}

// New returns a broker server. Serve validates required dependencies.
func New(opts Options) *Server {
	maxConnections := opts.MaxConnections
	if maxConnections <= 0 {
		maxConnections = defaultMaxConnections
	}

	wt := opts.WriteTimeout
	if wt <= 0 {
		wt = writeTimeout
	}

	return &Server{
		store:            opts.Store,
		socketPath:       opts.SocketPath,
		maxConnections:   maxConnections,
		writeTimeout:     wt,
		ready:            opts.Ready,
		onConnectionDone: opts.OnConnectionDone,
	}
}

// Serve accepts local requests until ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		return errContextNil
	}

	if s.store == nil {
		return errStoreNil
	}

	if s.socketPath == "" {
		return errSocketPathEmpty
	}

	listener, err := listenLocal(ctx, s.socketPath)
	if err != nil {
		return err
	}
	defer cleanupLocal(s.socketPath, listener)

	return s.serve(ctx, listener)
}

func (s *Server) serve(ctx context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)

	var connections sync.WaitGroup

	defer func() {
		cancel()
		connections.Wait()
	}()

	listenerCleanup := cancelclose.OnCancel(ctx, listener)

	defer func() {
		cancel()
		listenerCleanup()
	}()

	if s.ready != nil {
		close(s.ready)
	}

	semaphore := make(chan struct{}, s.maxConnections)

	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				break
			}

			return fmt.Errorf("accepting broker connection: %w", acceptErr)
		}

		select {
		case semaphore <- struct{}{}:
			connections.Go(func() {
				defer func() {
					<-semaphore

					if s.onConnectionDone != nil {
						s.onConnectionDone()
					}
				}()

				s.handleConnection(ctx, connection)
			})
		default:
			cleanup := cancelclose.OnCancel(ctx, connection)
			_ = s.writeResponse(connection, errorResponse("", broker.CodeBusy, broker.ErrBusy))

			cleanup()
		}
	}

	return nil
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn) {
	defer cancelclose.OnCancel(ctx, connection)()

	if err := connection.SetReadDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return
	}

	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, scannerInitialBytes), maxRequestBytes)

	if !scanner.Scan() {
		return
	}

	var request broker.Request
	if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
		_ = s.writeResponse(connection, errorResponse(request.ID, broker.CodeInvalidJSON, err))
		return
	}

	if err := validateRequest(request); err != nil {
		_ = s.writeResponse(connection, errorResponse(request.ID, broker.CodeInvalidRequest, err))
		return
	}

	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		return
	}

	if request.Method == broker.MethodSubscribe {
		s.serveSubscription(ctx, connection, request)
		return
	}

	response := s.execute(ctx, request)
	_ = s.writeResponse(connection, response)
}

func validateRequest(request broker.Request) error {
	if request.Version != broker.ProtocolVersion {
		return fmt.Errorf("%w: %d", errProtocolVersion, request.Version)
	}

	if request.ID == "" {
		return errRequestIDRequired
	}

	switch request.Method {
	case broker.MethodPing,
		broker.MethodObserve,
		broker.MethodObserveBatch,
		broker.MethodList,
		broker.MethodGet,
		broker.MethodSummary,
		broker.MethodGC,
		broker.MethodSubscribe,
		broker.MethodReset:
	default:
		return fmt.Errorf("%w: %q", errUnknownMethod, request.Method)
	}

	if request.Method == broker.MethodObserve && request.Observation == nil {
		return errObservationRequired
	}

	if request.Method == broker.MethodObserveBatch {
		if len(request.Observations) == 0 {
			return errBatchRequired
		}

		if len(request.Observations) > maxBatchObservations {
			return fmt.Errorf("%w: maximum %d", errBatchTooLarge, maxBatchObservations)
		}
	}

	if request.Method == broker.MethodGet && request.SessionID == "" {
		return errSessionIDRequired
	}

	return nil
}

func (s *Server) execute(ctx context.Context, request broker.Request) broker.Response {
	response := newResponse(request.ID, broker.ResponseResult)

	var err error

	switch request.Method {
	case broker.MethodPing:
		response.Now = time.Now().UTC()
	case broker.MethodObserve:
		var session registry.Session

		session, err = s.store.Observe(ctx, *request.Observation)
		response.Session = &session
	case broker.MethodObserveBatch:
		response.Sessions, err = s.store.ObserveBatch(ctx, request.Observations)
	case broker.MethodList:
		response.Sessions, err = s.store.List(ctx, request.Filter)
	case broker.MethodGet:
		var session registry.Session

		session, err = s.store.Get(ctx, request.SessionID)
		response.Session = &session
	case broker.MethodSummary:
		response.Summaries, err = s.store.SummaryWithOptions(
			ctx,
			request.Filter,
			request.SummaryOptions,
		)
	case broker.MethodGC:
		var result registry.GCResult

		result, err = s.store.GC(ctx, request.DeleteAfter)
		response.GC = &result
	case broker.MethodReset:
		var result registry.ResetResult

		result, err = s.store.Reset(ctx)
		response.Reset = &result
	}

	if err != nil {
		return operationErrorResponse(request.ID, err)
	}

	return response
}

func (s *Server) serveSubscription(
	ctx context.Context,
	connection net.Conn,
	request broker.Request,
) {
	ctx, cancel := context.WithCancel(ctx)
	cleanup := cancelclose.OnCancel(ctx, connection)
	done := make(chan struct{})

	go func() {
		defer close(done)

		_, _ = io.Copy(io.Discard, connection)

		cancel()
	}()

	defer func() {
		cancel()
		cleanup()
		<-done
	}()

	state, err := s.store.State(ctx, request.Filter)
	if err != nil {
		_ = s.writeResponse(connection, operationErrorResponse(request.ID, err))
		return
	}

	if err := s.writeResponse(connection, snapshotResponse(request.ID, state)); err != nil {
		return
	}

	revision := state.Revision

	for {
		waitCtx, cancel := context.WithTimeout(ctx, heartbeatInterval)
		next, waitErr := s.store.WaitForRevision(waitCtx, revision, request.Filter)

		cancel()

		if waitErr != nil {
			if ctx.Err() != nil {
				return
			}

			if errors.Is(waitErr, context.DeadlineExceeded) {
				response := newResponse(request.ID, broker.ResponseHeartbeat)

				response.Now = time.Now().UTC()
				if err := s.writeResponse(connection, response); err != nil {
					return
				}

				continue
			}

			_ = s.writeResponse(connection, operationErrorResponse(request.ID, waitErr))

			return
		}

		if err := s.writeResponse(connection, snapshotResponse(request.ID, next)); err != nil {
			return
		}

		revision = next.Revision
	}
}

func snapshotResponse(id string, state registry.StateSnapshot) broker.Response {
	response := newResponse(id, broker.ResponseSnapshot)
	response.Snapshot = &state

	return response
}

func errorResponse(id string, code string, err error) broker.Response {
	response := newResponse(id, broker.ResponseError)
	response.Error = &broker.Error{Code: code, Message: err.Error()}

	return response
}

func newResponse(id string, responseType string) broker.Response {
	return broker.Response{
		Version:   broker.ProtocolVersion,
		ID:        id,
		Type:      responseType,
		Error:     nil,
		Session:   nil,
		Sessions:  nil,
		Summaries: nil,
		Snapshot:  nil,
		GC:        nil,
		Reset:     nil,
		Now:       time.Time{},
	}
}

func operationErrorResponse(id string, err error) broker.Response {
	return errorResponse(id, broker.ErrorCode(err), err)
}

func (s *Server) writeResponse(connection net.Conn, response broker.Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encoding broker response: %w", err)
	}

	if len(encoded) >= broker.MaxResponseBytes {
		encoded, err = json.Marshal(
			errorResponse(response.ID, broker.CodeResponseTooLarge, errResponseTooLarge),
		)
		if err != nil {
			return fmt.Errorf("encoding oversized-response error: %w", err)
		}
	}

	if err := connection.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		return fmt.Errorf("setting broker write deadline: %w", err)
	}

	if _, err := connection.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("writing broker response: %w", err)
	}

	if err := connection.SetWriteDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clearing broker write deadline: %w", err)
	}

	return nil
}
