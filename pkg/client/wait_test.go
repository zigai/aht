package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/testdir"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestWaitReturnsDurableReadError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"schema_version":999,"sessions":{}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	c := client.New(client.Config{StorePath: path, Mode: client.ModeDurableOnly})

	_, err := c.Wait(t.Context(), client.WaitOptions{
		Reference: "session", Activity: registry.ActivityIdle, Timeout: time.Second,
	})
	if unsupported, ok := errors.AsType[*registry.UnsupportedSchemaError](
		err,
	); !ok ||
		unsupported.Version != 999 {
		t.Fatalf("durable read error = %v, want unsupported schema 999", err)
	}
}

func TestWaitValidation(t *testing.T) {
	t.Parallel()

	c := client.New(client.Config{})

	tests := []struct {
		name    string
		options client.WaitOptions
		wantErr error
	}{
		{
			name:    "empty session ID",
			options: client.WaitOptions{Reference: "", Activity: registry.ActivityIdle},
			wantErr: client.ErrSessionRequired,
		},
		{
			name:    "whitespace session ID",
			options: client.WaitOptions{Reference: "   ", Activity: registry.ActivityIdle},
			wantErr: client.ErrSessionRequired,
		},
		{
			name: "negative timeout",
			options: client.WaitOptions{
				Reference: "sess-1",
				Activity:  registry.ActivityIdle,
				Timeout:   -time.Second,
			},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name: "negative stable-for",
			options: client.WaitOptions{
				Reference: "sess-1",
				Activity:  registry.ActivityIdle,
				StableFor: -time.Second,
			},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name: "stable-for exceeds timeout",
			options: client.WaitOptions{
				Reference: "sess-1",
				Activity:  registry.ActivityIdle,
				Timeout:   time.Second,
				StableFor: 2 * time.Second,
			},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name: "stable-for equals timeout",
			options: client.WaitOptions{
				Reference: "sess-1",
				Activity:  registry.ActivityIdle,
				Timeout:   time.Second,
				StableFor: time.Second,
			},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name:    "empty condition",
			options: client.WaitOptions{Reference: "sess-1"},
			wantErr: client.ErrConditionRequired,
		},
		{
			name:    "invalid presence",
			options: client.WaitOptions{Reference: "sess-1", Presence: "bogus"},
			wantErr: registry.ErrUnknownPresence,
		},
		{
			name:    "invalid activity",
			options: client.WaitOptions{Reference: "sess-1", Activity: "bogus"},
			wantErr: registry.ErrUnknownActivity,
		},
		{
			name: "contradictory presence gone with activity",
			options: client.WaitOptions{
				Reference: "sess-1",
				Presence:  registry.PresenceGone,
				Activity:  registry.ActivityIdle,
			},
			wantErr: client.ErrContradictoryCondition,
		},
		{
			name: "contradictory presence unknown with activity",
			options: client.WaitOptions{
				Reference: "sess-1",
				Presence:  registry.PresenceUnknown,
				Activity:  registry.ActivityRunning,
			},
			wantErr: client.ErrContradictoryCondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := c.Wait(context.Background(), tt.options)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Wait() err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWaitBrokerDisconnect(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(testdir.Short(t, "aht-wait-"), "broker.sock")

	var config net.ListenConfig

	listener, err := config.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}

	served := make(chan error, 1)

	t.Cleanup(func() {
		_ = listener.Close()

		if err := <-served; err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})

	go func() {
		connection, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer func() { _ = connection.Close() }()

		var request broker.Request
		if err := json.NewDecoder(connection).Decode(&request); err != nil {
			served <- err
			return
		}

		session := registry.Session{
			ID: "session",
			Liveness: registry.NewLiveness(
				registry.PresenceLive,
				registry.ActivityValue(new(registry.ActivityRunning)),
				nil,
				time.Time{},
				"",
			),
		}
		if err := json.NewEncoder(connection).Encode(broker.Response{
			Version:  broker.ProtocolVersion,
			ID:       request.ID,
			Type:     broker.ResponseSnapshot,
			Snapshot: &registry.StateSnapshot{Revision: 1, Sessions: []registry.Session{session}},
		}); err != nil {
			served <- err
			return
		}

		served <- nil
	}()

	c := client.New(client.Config{SocketPath: socket, Mode: client.ModeRealtimeOnly})

	_, err = c.Wait(
		t.Context(),
		client.WaitOptions{
			Reference: "session",
			Activity:  registry.ActivityIdle,
			Timeout:   5 * time.Second,
		},
	)
	if !broker.StreamClosed(err) || broker.IsUnavailable(err) {
		t.Fatalf("Wait after disconnect = %v, want stream closed", err)
	}
}

func TestWaitDurableWatchBehavior(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})
	observation := runningObservation("durable-session")

	observed, err := store.Observe(t.Context(), observation)
	if err != nil {
		t.Fatal(err)
	}

	c := client.New(client.Config{StorePath: storePath, Mode: client.ModeDurableOnly})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	snapshots := make(chan registry.StateSnapshot, 2)

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

	initial := receiveSnapshot(t, snapshots)
	if len(initial.Sessions) != 1 || initial.Sessions[0].ID != observed.ID ||
		initial.Sessions[0].Activity() == nil || *initial.Sessions[0].Activity() != registry.ActivityRunning {
		t.Fatalf("initial durable snapshot = %+v", initial)
	}

	observation.At = observation.At.Add(time.Second)

	observation.Evidence = &registry.Report{
		Claim:    new(registry.PresenceLive),
		Activity: new(registry.ActivityIdle),
	}
	if _, err := store.Observe(t.Context(), observation); err != nil {
		t.Fatal(err)
	}

	idle := receiveSnapshot(t, snapshots)
	if len(idle.Sessions) != 1 || idle.Sessions[0].ID != observed.ID ||
		idle.Sessions[0].Activity() == nil || *idle.Sessions[0].Activity() != registry.ActivityIdle {
		t.Fatalf("idle snapshot = %+v", idle)
	}

	result, err := c.Wait(
		t.Context(),
		client.WaitOptions{
			Reference: observed.ID,
			Activity:  registry.ActivityIdle,
			Timeout:   5 * time.Second,
		},
	)
	if err != nil || !result.Initial || result.Session.ID != observed.ID {
		t.Fatalf("Wait on durable idle session = %+v, %v", result, err)
	}
}
