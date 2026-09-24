//go:build linux || darwin

package brokerserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

type monitoredConn struct {
	net.Conn

	onWrite   func()
	onClose   func()
	closeOnce sync.Once
}

func (m *monitoredConn) Write(b []byte) (int, error) {
	if m.onWrite != nil {
		m.onWrite()
	}
	n, err := m.Conn.Write(b)
	if err != nil {
		return n, fmt.Errorf("monitored conn write: %w", err)
	}
	return n, nil
}

func (m *monitoredConn) Close() error {
	err := m.Conn.Close()
	m.closeOnce.Do(func() {
		if m.onClose != nil {
			m.onClose()
		}
	})
	if err != nil {
		return fmt.Errorf("monitored conn close: %w", err)
	}
	return nil
}

type testPipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	close sync.Once
}

func newTestPipeListener() *testPipeListener {
	return &testPipeListener{
		conns: make(chan net.Conn, 8),
		done:  make(chan struct{}),
	}
}

func (p *testPipeListener) push(c net.Conn) {
	p.conns <- c
}

func (p *testPipeListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-p.conns:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	case <-p.done:
		return nil, net.ErrClosed
	}
}

func (p *testPipeListener) Close() error {
	p.close.Do(func() {
		close(p.done)
	})
	return nil
}

func (p *testPipeListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "pipe", Net: "unix"}
}

//nolint:cyclop // One end-to-end scenario verifies transport, permissions, and streaming.
func TestServerStreamsEffectiveStateChanges(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	path, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(broker.SocketPath(path))
	})
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	server := New(Options{
		Store:      store,
		SocketPath: broker.SocketPath(path),
		Ready:      ready,
	})
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(ctx) }()

	select {
	case err := <-serverErrors:
		t.Fatalf("server exited before readiness: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-ready:
	}

	info, err := os.Stat(broker.SocketPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o, want 600", info.Mode().Perm())
	}

	client := broker.NewClient(path)
	subscription, err := client.Subscribe(ctx, registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	initial := receiveSnapshot(t, ctx, subscription.Snapshots)
	if initial.Revision != 1 || len(initial.Sessions) != 0 {
		t.Fatalf("initial snapshot = %#v, want empty revision 1", initial)
	}

	presence := registry.PresenceLive
	running := registry.ActivityRunning
	observation := registry.Observation{Harness: registry.Harness("omp"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "broker-live"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: "omp-extension"}, Event: "agent_start", Claim: &presence, Activity: &running}}
	if _, err := client.Observe(ctx, observation); err != nil {
		t.Fatal(err)
	}

	update := receiveSnapshot(t, ctx, subscription.Snapshots)
	if update.Revision != 2 || len(update.Sessions) != 1 || update.Sessions[0].Activity() == nil || *update.Sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("update snapshot = %#v, want one running session at revision 2", update)
	}

	cancel()
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func receiveSnapshot(
	t *testing.T,
	ctx context.Context,
	snapshots <-chan registry.StateSnapshot,
) registry.StateSnapshot {
	t.Helper()

	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return registry.StateSnapshot{}
	case snapshot, ok := <-snapshots:
		if !ok {
			t.Fatal("subscription closed before snapshot")
		}
		return snapshot
	}
}

// shortStatePath keeps the derived socket below Darwin's Unix socket path limit.
func shortStatePath() (string, error) {
	stateFile, err := os.CreateTemp("", "aht-broker-state-")
	if err != nil {
		return "", fmt.Errorf("creating temporary state path: %w", err)
	}
	path := stateFile.Name()
	if err := stateFile.Close(); err != nil {
		return "", fmt.Errorf("closing temporary state file: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("removing temporary state file: %w", err)
	}
	return path, nil
}

type brokerAdversityFixture struct {
	path   string
	store  *registry.MemoryStore
	cancel context.CancelFunc
	done   <-chan error
}

func TestServer_RejectsProtocolAdversity(t *testing.T) {
	fixture := startBrokerAdversityFixture(t)
	tests := []struct {
		name        string
		payload     []byte
		wantCode    string
		wantMessage string
	}{
		{
			name:        "malformed JSON",
			payload:     []byte("{not-json}\n"),
			wantCode:    "invalid_json",
			wantMessage: "invalid character",
		},
		{
			name:        "unsupported version",
			payload:     marshalBrokerRequest(t, broker.Request{Version: broker.ProtocolVersion + 1, ID: "wrong-version", Method: broker.MethodPing}),
			wantCode:    "invalid_request",
			wantMessage: "unsupported broker protocol version",
		},
		{
			name: "oversized batch",
			payload: marshalBrokerRequest(t, broker.Request{
				Version:      broker.ProtocolVersion,
				ID:           "large-batch",
				Method:       broker.MethodObserveBatch,
				Observations: make([]registry.Observation, maxBatchObservations+1),
			}),
			wantCode:    "invalid_request",
			wantMessage: "too many observations",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := sendRawBrokerRequest(t, fixture.path, test.payload)
			if response.Type != "error" || response.Error == nil || response.Error.Code != test.wantCode || !strings.Contains(response.Error.Message, test.wantMessage) {
				t.Fatalf("response = %#v, want %q containing %q", response, test.wantCode, test.wantMessage)
			}
			state, err := fixture.store.State(t.Context(), registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if state.Revision != 1 || len(state.Sessions) != 0 {
				t.Fatalf("invalid request mutated state: %#v", state)
			}
		})
	}

	if err := broker.NewClient(fixture.path).Ping(t.Context()); err != nil {
		t.Fatalf("broker did not serve a valid request after adversity: %v", err)
	}
	stopBrokerAdversityFixture(t, fixture)
}

func TestServer_BoundsSlowSubscriber(t *testing.T) {
	fixture := startBrokerAdversityFixture(t)
	connection := dialRawBroker(t, fixture.path)
	defer func() { _ = connection.Close() }()
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		_ = unixConnection.SetReadBuffer(1)
	}
	subscribe := marshalBrokerRequest(t, broker.Request{
		Version: broker.ProtocolVersion,
		ID:      "slow-subscriber",
		Method:  broker.MethodSubscribe,
	})
	if _, err := connection.Write(subscribe); err != nil {
		t.Fatalf("write slow subscription: %v", err)
	}

	padding := strings.Repeat("x", 1<<20)
	presence := registry.PresenceLive
	for index := range 4 {
		_, err := fixture.store.Observe(t.Context(), registry.Observation{Harness: registry.Harness("omp"), At: time.Now().UTC().Add(time.Duration(index) * time.Nanosecond), Subject: registry.ObservationIdentity{SessionID: fmt.Sprintf("slow-%d", index)}, Evidence: &registry.Report{Event: "agent_start", Claim: &presence, Attributes: map[string]string{"padding": padding}}})
		if err != nil {
			t.Fatal(err)
		}
	}

	clientContext, cancelClient := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelClient()
	client := broker.NewClient(fixture.path)
	if err := client.Ping(clientContext); err != nil {
		t.Fatalf("slow subscriber blocked another client: %v", err)
	}
	session, err := client.Observe(clientContext, registry.Observation{Harness: registry.Harness("omp"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "healthy-client"}, Evidence: &registry.Report{Event: "agent_start", Claim: &presence}})
	if err != nil || session.SessionID != "healthy-client" {
		t.Fatalf("healthy client observe = %#v, %v", session, err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("disconnect slow subscriber: %v", err)
	}
	if err := client.Ping(clientContext); err != nil {
		t.Fatalf("disconnected subscriber affected broker: %v", err)
	}

	testSlowSubscriberBlockedWriteTimeout(t, fixture.store)

	stopBrokerAdversityFixture(t, fixture)
}

func testSlowSubscriberBlockedWriteTimeout(t *testing.T, store *registry.MemoryStore) {
	t.Helper()

	serverPipe, clientPipe := net.Pipe()
	writeStarted := make(chan struct{})
	serverClosed := make(chan struct{})
	var writeOnce, closeOnce sync.Once
	monitored := &monitoredConn{
		Conn: serverPipe,
		onWrite: func() {
			writeOnce.Do(func() { close(writeStarted) })
		},
		onClose: func() {
			closeOnce.Do(func() { close(serverClosed) })
		},
		closeOnce: sync.Once{},
	}

	listener := newTestPipeListener()
	pipeCtx, cancelPipe := context.WithCancel(t.Context())

	connectionDone := make(chan struct{}, 1)
	serverDone := make(chan struct{})
	slowServer := New(Options{
		Store:          store,
		SocketPath:     "",
		MaxConnections: 1,
		WriteTimeout:   50 * time.Millisecond,
		Ready:          nil,
		OnConnectionDone: func() {
			select {
			case connectionDone <- struct{}{}:
			default:
			}
		},
	})
	go func() {
		defer close(serverDone)
		_ = slowServer.serve(pipeCtx, listener)
	}()
	t.Cleanup(func() {
		cancelPipe()
		_ = listener.Close()
		<-serverDone
	})

	listener.push(monitored)

	subReq := marshalBrokerRequest(t, broker.Request{
		Version: broker.ProtocolVersion,
		ID:      "blocked-sub",
		Method:  broker.MethodSubscribe,
	})
	clientWriteDone := make(chan struct{})
	go func() {
		defer close(clientWriteDone)
		_, _ = clientPipe.Write(subReq)
	}()
	t.Cleanup(func() {
		_ = clientPipe.Close()
		<-clientWriteDone
	})

	// Wait for server to initiate write (which blocks because clientPipe is not reading)
	select {
	case <-writeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("server never initiated write on slow connection")
	}

	// Observe server-side close triggered by write timeout, while clientPipe remains unread
	select {
	case <-serverClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("server failed to time out and close slow connection")
	}

	// Only read from clientPipe after the server has closed its end
	_ = clientPipe.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, err := clientPipe.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("slow connection read after close = %d bytes, error %v; want io.EOF", n, err)
	}

	// Wait for the admission semaphore slot to be released (avoids admission-release race)
	select {
	case <-connectionDone:
	case <-time.After(5 * time.Second):
		t.Fatal("connection slot was not released after close")
	}

	// Verify the connection slot was released: accept another connection despite MaxConnections=1
	testSecondClientAdmissionPing(t, listener)

	cancelPipe()
	_ = listener.Close()
	<-serverDone

	_ = clientPipe.Close()
	<-clientWriteDone
}

func testSecondClientAdmissionPing(t *testing.T, listener *testPipeListener) {
	t.Helper()

	serverPipe, clientPipe := net.Pipe()
	writeDone := make(chan struct{})
	pingReq := marshalBrokerRequest(t, broker.Request{
		Version: broker.ProtocolVersion,
		ID:      "ping-after-timeout",
		Method:  broker.MethodPing,
	})
	go func() {
		defer close(writeDone)
		_, _ = clientPipe.Write(pingReq)
	}()
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			_ = clientPipe.Close()
			<-writeDone
		})
	}
	t.Cleanup(cleanup)
	defer cleanup()

	listener.push(serverPipe)

	// Bound I/O on second client with SetDeadline
	if err := clientPipe.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("second client set deadline: %v", err)
	}

	scanner := bufio.NewScanner(clientPipe)
	if !scanner.Scan() {
		t.Fatalf("second client ping scan: %v", scanner.Err())
	}
	var resp broker.Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil || resp.ID != "ping-after-timeout" {
		t.Fatalf("second client ping response = %#v, %v", resp, err)
	}
}

func TestServer_ShutsDownCleanly(t *testing.T) {
	fixture := startBrokerAdversityFixture(t)
	connection := dialRawBroker(t, fixture.path)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(connection, "{"); err != nil {
		t.Fatalf("write partial handshake: %v", err)
	}

	fixture.cancel()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-fixture.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("broker did not close a partial-handshake connection during shutdown")
	}
	if _, err := os.Stat(broker.SocketPath(fixture.path)); !os.IsNotExist(err) {
		t.Fatalf("broker socket remains after shutdown: %v", err)
	}
}

func startBrokerAdversityFixture(t *testing.T) brokerAdversityFixture {
	t.Helper()
	path, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(broker.SocketPath(path))
	})
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan struct{})
	done := make(chan error, 1)
	server := New(Options{Store: store, SocketPath: broker.SocketPath(path), Ready: ready})
	go func() { done <- server.Serve(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("server exited before readiness: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-ready:
	}
	return brokerAdversityFixture{path: path, store: store, cancel: cancel, done: done}
}

func stopBrokerAdversityFixture(t *testing.T, fixture brokerAdversityFixture) {
	t.Helper()
	fixture.cancel()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case err := <-fixture.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("broker did not stop within its connection deadline")
	}
}

func marshalBrokerRequest(t *testing.T, request broker.Request) []byte {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return append(payload, '\n')
}

func dialRawBroker(t *testing.T, storePath string) net.Conn {
	t.Helper()
	dialer := net.Dialer{Timeout: time.Second}
	connection, err := dialer.DialContext(t.Context(), "unix", broker.SocketPath(storePath))
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func sendRawBrokerRequest(t *testing.T, storePath string, payload []byte) broker.Response {
	t.Helper()
	connection := dialRawBroker(t, storePath)
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(payload); err != nil {
		t.Fatal(err)
	}
	var response broker.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatalf("decode broker response: %v", err)
	}
	return response
}

func BenchmarkBrokerObserveRoundTrip(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(b.TempDir(), "state.json")
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		b.Fatal(err)
	}
	ready := make(chan struct{})
	server := New(Options{
		Store:      store,
		SocketPath: broker.SocketPath(path),
		Ready:      ready,
	})
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(ctx) }()
	<-ready

	client := broker.NewClient(path)
	running := registry.ActivityRunning
	sequence := uint64(0)
	observation := registry.Observation{Harness: registry.Harness("omp"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "benchmark"}, Evidence: &registry.Report{Reporter: registry.Reporter{Integration: "omp-extension", Sequence: &sequence}, Event: "agent_start", Activity: &running}}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sequence++
		observation.At = observation.At.Add(time.Nanosecond)
		if _, err := client.Observe(ctx, observation); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	cancel()
	if err := <-serverErrors; err != nil {
		b.Fatal(err)
	}
}

func TestServerSummaryGroupingAndParity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	path, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	server := New(Options{
		Store:      store,
		SocketPath: broker.SocketPath(path),
		Ready:      ready,
	})
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(ctx) }()
	<-ready

	client := broker.NewClient(path)
	running := registry.ActivityRunning

	obs1 := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-1"}, Evidence: &registry.Report{Claim: new(registry.PresenceLive), Activity: &running, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "main"}, Listing: &registry.Listing{ProjectRoot: "/repo/one"}}}
	obs2 := registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-2"}, Evidence: &registry.Report{Claim: new(registry.PresenceLive), Activity: &running, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "other"}, Listing: &registry.Listing{ProjectRoot: "/repo/two"}}}

	if _, err := client.ObserveBatch(ctx, []registry.Observation{obs1, obs2}); err != nil {
		t.Fatal(err)
	}

	for _, groupBy := range []registry.SummaryGroupBy{
		registry.SummaryGroupByMultiplexerSession,
		registry.SummaryGroupByProject,
		registry.SummaryGroupByHarness,
	} {
		opts := registry.SummaryOptions{GroupBy: groupBy}
		brokerSummaries, err := client.SummaryWithOptions(ctx, registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("client summary error for %s: %v", groupBy, err)
		}
		memSummaries, err := store.SummaryWithOptions(ctx, registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("store summary error for %s: %v", groupBy, err)
		}
		assertBrokerParity(t, groupBy, brokerSummaries, memSummaries)
	}

	// Test default Summary call (without options) defaults to multiplexer session
	defaultSum, err := client.Summary(ctx, registry.Filter{})
	if err != nil {
		t.Fatalf("default summary error: %v", err)
	}
	muxSum, err := client.SummaryWithOptions(ctx, registry.Filter{}, registry.SummaryOptions{GroupBy: registry.SummaryGroupByMultiplexerSession})
	if err != nil {
		t.Fatalf("mux summary error: %v", err)
	}
	if len(defaultSum) != len(muxSum) {
		t.Fatalf("default summary len %d != mux summary len %d", len(defaultSum), len(muxSum))
	}

	// Test invalid group by returns error
	_, err = client.SummaryWithOptions(ctx, registry.Filter{}, registry.SummaryOptions{GroupBy: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid group by, got nil")
	}

	cancel()
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func assertBrokerParity(t *testing.T, groupBy registry.SummaryGroupBy, brokerSummaries, memSummaries []registry.Summary) {
	t.Helper()
	if len(brokerSummaries) != len(memSummaries) {
		t.Fatalf("len mismatch for %s: broker=%d mem=%d", groupBy, len(brokerSummaries), len(memSummaries))
	}
	for i := range brokerSummaries {
		if brokerSummaries[i].GroupKey != memSummaries[i].GroupKey ||
			brokerSummaries[i].GroupLabel != memSummaries[i].GroupLabel ||
			brokerSummaries[i].Total != memSummaries[i].Total {
			t.Fatalf("mismatch for %s item %d: broker=%+v mem=%+v", groupBy, i, brokerSummaries[i], memSummaries[i])
		}
	}
}

func TestListenerFullBacklogDoesNotDeleteLiveSocket(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "aht-busy-") //nolint:usetesting // reason: Unix domain socket paths in /tmp must stay short to prevent sockaddr_un overflow on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })
	path := filepath.Join(tempDir, "busy.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(fd) }()
	if err = syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	} // Tiny queue to reproduce overload deterministically.
	conn, err := new(net.Dialer).DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	replacement, err := listenLocal(context.Background(), path)
	if replacement != nil {
		defer func() { _ = replacement.Close() }()
		t.Fatal("BUG: live listening socket with full backlog was unlinked and replaced by another broker listener")
	}
	if err == nil {
		t.Fatal("expected refusal while original listener exists")
	}
}
