package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

var errOther = errors.New("other error")

var _ registry.Store = (*broker.Client)(nil)

func TestClientOfflineReturnsUnavailable(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")
	client := broker.NewClientForSocket(socketPath)

	if client.SocketPath() != socketPath {
		t.Fatalf("SocketPath() = %q, want %q", client.SocketPath(), socketPath)
	}

	err := client.Ping(t.Context())
	if !broker.IsUnavailable(err) {
		t.Fatalf("Ping() err = %v, want ErrUnavailable", err)
	}

	_, err = client.List(t.Context(), registry.Filter{})
	if !broker.IsUnavailable(err) {
		t.Fatalf("List() err = %v, want ErrUnavailable", err)
	}

	_, err = client.Subscribe(t.Context(), registry.Filter{})
	if !broker.IsUnavailable(err) {
		t.Fatalf("Subscribe() err = %v, want ErrUnavailable", err)
	}
}

func TestIsUnavailable(t *testing.T) {
	t.Parallel()

	if !broker.IsUnavailable(broker.ErrUnavailable) {
		t.Fatal("IsUnavailable(ErrUnavailable) = false, want true")
	}

	wrapped := fmt.Errorf("something: %w", broker.ErrUnavailable)
	if !broker.IsUnavailable(wrapped) {
		t.Fatal("IsUnavailable(wrapped) = false, want true")
	}
	if broker.IsUnavailable(errOther) {
		t.Fatal("IsUnavailable(other) = true, want false")
	}
	if !broker.IsUnavailable(io.EOF) {
		t.Fatal("IsUnavailable(io.EOF) = false, want true")
	}
	if !broker.IsUnavailable(fmt.Errorf("reading broker response: %w", io.EOF)) {
		t.Fatal("IsUnavailable(wrapped io.EOF) = false, want true")
	}
}

func TestClientMissingResponsePayloadFailsWithProtocol(t *testing.T) {
	t.Parallel()

	operations := map[string]func(ctx context.Context, client *broker.Client) error{
		"get": func(ctx context.Context, client *broker.Client) error {
			_, err := client.Get(ctx, "session-1")
			if err != nil {
				return fmt.Errorf("get: %w", err)
			}
			return nil
		},
		"observe": func(ctx context.Context, client *broker.Client) error {
			_, err := client.Observe(ctx, registry.Observation{})
			if err != nil {
				return fmt.Errorf("observe: %w", err)
			}
			return nil
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := responseServer(t, func(id string) broker.Response {
				return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "result"}
			})

			if err := operation(t.Context(), client); !errors.Is(err, broker.ErrProtocol) {
				t.Fatalf("%s error = %v, want ErrProtocol", name, err)
			}
		})
	}
}

func TestClientEmptyListAndSummaryReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	client := responseServer(t, func(id string) broker.Response {
		return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "result"}
	})

	sessions, err := client.List(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("List len = %d, want 0", len(sessions))
	}
}

// responseServer starts a one-shot Unix-socket broker that decodes the first
// request and replies with respond. It skips where Unix sockets are unavailable.
func responseServer(t *testing.T, respond func(id string) broker.Response) *broker.Client {
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
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		resp := respond(request.ID)
		encoded, marshalErr := json.Marshal(resp)
		if marshalErr != nil {
			return
		}
		encoded = append(encoded, '\n')
		_, _ = connection.Write(encoded)
	}()

	return broker.NewClientForSocket(socketPath)
}
