package client_test

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

func TestInvalidModeRejectsOperations(t *testing.T) {
	c := client.New(client.Config{Mode: "typo", StorePath: filepath.Join(t.TempDir(), "missing.json")})
	ctx := t.Context()
	_, listErr := c.List(ctx, registry.Filter{})
	_, getErr := c.Get(ctx, "missing")
	_, observeErr := c.Observe(ctx, registry.Observation{})
	_, batchErr := c.ObserveBatch(ctx, nil)
	_, summaryErr := c.Summary(ctx, registry.Filter{})
	_, gcErr := c.GC(ctx, 0)
	_, subscribeErr := c.Subscribe(ctx, registry.Filter{})
	for operation, err := range map[string]error{
		"list": listErr, "get": getErr, "observe": observeErr,
		"batch": batchErr, "summary": summaryErr, "gc": gcErr,
		"subscribe": subscribeErr, "ping": c.Ping(ctx),
	} {
		if !errors.Is(err, client.ErrInvalidMode) {
			t.Errorf("%s error = %v, want ErrInvalidMode", operation, err)
		}
	}
}

func TestModeAutoFallsBackWhenBrokerClosesConnection(t *testing.T) {
	t.Parallel()

	socketPath := brokerClosingConnections(t)
	storePath := filepath.Join(t.TempDir(), "registry.json")
	c := client.New(client.Config{Mode: client.ModeAuto, StorePath: storePath, SocketPath: socketPath})

	sessions, err := c.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatalf("List() err = %v, want durable fallback", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("List() = %d sessions, want 0 durable sessions", len(sessions))
	}
}

// brokerClosingConnections starts a one-shot Unix-socket broker that drains the
// first request and closes the connection without answering. It skips where
// Unix sockets are unavailable.
func brokerClosingConnections(t *testing.T) string {
	t.Helper()

	socketFile, err := os.CreateTemp(t.TempDir(), "aht-broker-")
	if err != nil {
		t.Skipf("creating temporary socket path: %v", err)
	}
	socketPath := socketFile.Name()
	_ = socketFile.Close()
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", socketPath)
	if err != nil {
		t.Skipf("listening on unix socket: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))

		var request struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(connection).Decode(&request)
	}()

	return socketPath
}
