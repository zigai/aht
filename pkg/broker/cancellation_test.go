//go:build linux || darwin

package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/registry"
)

func TestRequestsCancelBlockedResponse(t *testing.T) {
	operations := map[string]func(context.Context, *broker.Client) error{
		"request": func(ctx context.Context, client *broker.Client) error {
			return client.Ping(ctx)
		},
		"subscribe": func(ctx context.Context, client *broker.Client) error {
			_, err := client.Subscribe(ctx, registry.Filter{})
			//nolint:wrapcheck // The test operation preserves the client's exact cancellation contract.
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			requestRead := make(chan struct{})
			client := socketClient(t, func(connection net.Conn) {
				var request struct{}
				if json.NewDecoder(connection).Decode(&request) == nil {
					close(requestRead)
				}
				_, _ = io.Copy(io.Discard, connection)
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- operation(ctx, client)
			}()
			select {
			case <-requestRead:
			case <-time.After(time.Second):
				t.Fatal("request was not received")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled request remained blocked")
			}
		})
	}
}

func TestSubscriptionCloseJoinsReader(t *testing.T) {
	client := socketClient(t, func(connection net.Conn) {
		var request struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		_ = json.NewEncoder(connection).Encode(broker.Response{
			Version: broker.ProtocolVersion, ID: request.ID, Type: "snapshot",
			Snapshot: &registry.StateSnapshot{},
		})
		_, _ = io.Copy(io.Discard, connection)
	})
	subscription, err := client.Subscribe(t.Context(), registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	<-subscription.Snapshots
	subscription.Close()

	select {
	case _, ok := <-subscription.Snapshots:
		if ok {
			t.Fatal("unexpected snapshot after close")
		}
	default:
		t.Fatal("Close returned before reader closed snapshots")
	}
}

func socketClient(t *testing.T, serve func(net.Conn)) *broker.Client {
	t.Helper()
	path, err := shortSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
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
		serve(connection)
	}()
	return broker.NewClientForSocket(path)
}

// shortSocketPath keeps test listeners below Darwin's Unix socket path limit.
func shortSocketPath() (string, error) {
	socketFile, err := os.CreateTemp("", "aht-broker-")
	if err != nil {
		return "", fmt.Errorf("creating temporary socket path: %w", err)
	}
	path := socketFile.Name()
	if err := socketFile.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("closing temporary socket path: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("removing temporary socket path: %w", err)
	}
	return path, nil
}
