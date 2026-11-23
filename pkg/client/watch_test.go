package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestAutoWatchReturnsToBrokerAfterOutage(t *testing.T) {
	t.Parallel()

	storePath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}

	socketPath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}

	store, err := registry.OpenMemoryStore(storePath, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	c := client.New(
		client.Config{StorePath: storePath, SocketPath: socketPath, Mode: client.ModeAuto},
	)
	snapshots := make(chan registry.StateSnapshot, 64)
	done := make(chan error, 1)

	go func() {
		done <- c.Watch(ctx, registry.Filter{}, func(snapshot registry.StateSnapshot) error {
			select {
			case snapshots <- snapshot:
			case <-ctx.Done():
			}

			return nil
		})
	}()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	// Durable snapshots carry no revision; broker snapshots always do.
	if snapshot := receiveSnapshot(t, snapshots); snapshot.Revision != 0 {
		t.Fatalf("snapshot without a broker has revision %d, want durable", snapshot.Revision)
	}

	stop := startErrorContractBroker(t, store, socketPath)
	requireSessionFrom(t, store, snapshots, "first-outage", true)

	stop()
	requireSessionFrom(t, store, snapshots, "during-outage", false)

	startErrorContractBroker(t, store, socketPath)
	requireSessionFrom(t, store, snapshots, "second-return", true)
}

func requireSessionFrom(
	t *testing.T,
	store *registry.MemoryStore,
	snapshots <-chan registry.StateSnapshot,
	sessionID string,
	realtime bool,
) {
	t.Helper()

	if _, err := store.Observe(t.Context(), runningObservation(sessionID)); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(10 * time.Second)

	for {
		select {
		case snapshot := <-snapshots:
			if (snapshot.Revision > 0) != realtime {
				continue
			}

			for _, session := range snapshot.Sessions {
				if session.SessionID == sessionID {
					return
				}
			}
		case <-deadline:
			t.Fatalf("no snapshot with %q from the broker=%t", sessionID, realtime)
		}
	}
}
