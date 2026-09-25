//go:build linux || darwin

package brokerserver

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errAcceptFailed = errors.New("accept failed")

type failingListener struct {
	net.Listener

	fail     <-chan struct{}
	accepted bool
}

func (l *failingListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		//nolint:wrapcheck // This listener delegates the first accept unchanged and injects only the next failure.
		return l.Listener.Accept()
	}
	<-l.fail
	return nil, errAcceptFailed
}

func TestAcceptFailureStopsActiveSubscriptions(t *testing.T) {
	path, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	socketPath := broker.SocketPath(path)
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(socketPath)
	})
	store, err := registry.OpenMemoryStore(path, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	fail := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := New(Options{Store: store, SocketPath: socketPath})
	result := make(chan error, 1)
	go func() {
		result <- server.serve(ctx, &failingListener{Listener: listener, fail: fail})
	}()
	subscription, err := broker.NewClient(path).Subscribe(ctx, registry.Filter{})
	close(fail)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	<-subscription.Snapshots
	select {
	case err := <-result:
		if !errors.Is(err, errAcceptFailed) {
			t.Fatalf("Serve error = %v, want accept failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not join active connections after accept failure")
	}
	select {
	case _, ok := <-subscription.Snapshots:
		if ok {
			t.Fatal("unexpected snapshot after server exit")
		}
	case <-time.After(time.Second):
		t.Fatal("server returned with subscription connection still active")
	}
}
