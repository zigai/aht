package fakeprovider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
)

const (
	defaultAddress = "127.0.0.1:0"
	defaultModel   = "fake"
)

var (
	errEmptySideMarker = errors.New("side marker must not be empty")
	errNegativeCount   = errors.New("request count must not be negative")
	errServerClosed    = errors.New("fake provider is closed")
)

// Options configures a [Server].
type Options struct {
	Address     string   // listen address; default "127.0.0.1:0"
	Model       string   // model id served by /models; default "fake"
	Script      Script   // default Echo()
	Hold        bool     // hold every conversation request at StageBeforeReply
	SideMarkers []string // extra body substrings that mark a side request
}

// Server is a fake model API. It is safe for concurrent use.
type Server struct {
	model       string
	sideMarkers []string
	holdAll     bool
	url         string
	httpServer  *http.Server
	serveDone   chan error
	holds       chan *Hold
	closing     chan struct{}
	handlers    sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	session *session
	changed chan struct{}
}

type session struct {
	script    Script
	requests  []Request
	side      []Request
	err       error
	abandoned chan struct{}
}

// Start listens on options.Address and serves until Close.
func Start(options Options) (*Server, error) {
	address := options.Address
	if address == "" {
		address = defaultAddress
	}
	model := options.Model
	if model == "" {
		model = defaultModel
	}
	script := options.Script
	if script == nil {
		script = Echo()
	}
	if slices.Contains(options.SideMarkers, "") {
		return nil, errEmptySideMarker
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for fake provider: %w", err)
	}
	server := &Server{
		model:       model,
		sideMarkers: slices.Clone(options.SideMarkers),
		holdAll:     options.Hold,
		url:         "http://" + listener.Addr().String(),
		httpServer:  nil,
		serveDone:   make(chan error, 1),
		holds:       make(chan *Hold),
		closing:     make(chan struct{}),
		handlers:    sync.WaitGroup{},
		mu:          sync.Mutex{},
		closed:      false,
		session:     newSession(script),
		changed:     make(chan struct{}),
	}
	//nolint:exhaustruct_v5,gosec // Loopback test provider without timeouts: the test decides how long held requests and streams stay open.
	server.httpServer = &http.Server{Handler: server}
	go func() {
		err := server.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		server.serveDone <- err
	}()
	return server, nil
}

func newSession(script Script) *session {
	return &session{
		script:    script,
		requests:  nil,
		side:      nil,
		err:       nil,
		abandoned: make(chan struct{}),
	}
}

// URL returns the server origin, without /v1.
func (s *Server) URL() string {
	return s.url
}

// Close stops the server and releases held requests.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closing)
	s.mu.Unlock()

	closeErr := s.httpServer.Close()
	s.handlers.Wait()
	serveErr := <-s.serveDone
	return errors.Join(closeErr, serveErr)
}

// Requests returns the conversation requests of the current session.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRequests(s.session.requests)
}

// SideRequests returns the side requests of the current session.
func (s *Server) SideRequests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRequests(s.session.side)
}

// WaitRequests waits until the current session has count conversation requests.
func (s *Server) WaitRequests(ctx context.Context, count int) ([]Request, error) {
	if count < 0 {
		return nil, fmt.Errorf("%w: %d", errNegativeCount, count)
	}
	for {
		s.mu.Lock()
		if len(s.session.requests) >= count {
			requests := cloneRequests(s.session.requests[:count])
			s.mu.Unlock()
			return requests, nil
		}
		changed := s.changed
		s.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for %d requests: %w", count, ctx.Err())
		case <-s.closing:
			return nil, fmt.Errorf("wait for %d requests: %w", count, errServerClosed)
		}
	}
}

// Err returns the first script or protocol error of the current session.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session.err
}

// Reset starts a new session with script, or Echo when nil.
func (s *Server) Reset(script Script) {
	if script == nil {
		script = Echo()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.session.abandoned)
	s.session = newSession(script)
	s.notifyLocked()
}

// Holds delivers each paused request.
func (s *Server) Holds() <-chan *Hold {
	return s.holds
}

func (s *Server) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.handlers.Add(1)
	return true
}

func (s *Server) current() *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *Server) recordConversation(request Request) (*session, Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen := s.session
	request.Kind = KindConversation
	request.Index = len(gen.requests)
	request.Outcome = OutcomePending
	gen.requests = append(gen.requests, request)
	s.notifyLocked()
	return gen, request
}

func (s *Server) recordSide(request Request) (*session, Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen := s.session
	request.Kind = KindSide
	request.Index = len(gen.side)
	request.Outcome = OutcomePending
	gen.side = append(gen.side, request)
	return gen, request
}

func (s *Server) setOutcome(gen *session, request Request, outcome Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Kind == KindSide {
		gen.side[request.Index].Outcome = outcome
		return
	}
	gen.requests[request.Index].Outcome = outcome
}

func (s *Server) fail(gen *session, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen.err == nil {
		gen.err = err
	}
}

func (s *Server) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func cloneRequests(requests []Request) []Request {
	clones := make([]Request, len(requests))
	for i, request := range requests {
		clones[i] = request.clone()
	}
	return clones
}
