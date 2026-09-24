package client_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/internal/brokerserver"
	catalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/broker"
	"github.com/zigai/aht/pkg/client"
	"github.com/zigai/aht/pkg/registry"
)

func TestClientListFallsBackToDurableRegistry(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})
	if _, err := store.Observe(t.Context(), runningObservation("fallback")); err != nil {
		t.Fatal(err)
	}

	sessions, err := client.New(client.Config{StorePath: storePath}).List(
		t.Context(),
		registry.Filter{Presence: registry.PresenceLive},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "fallback" {
		t.Fatalf("List() = %#v, want fallback session", sessions)
	}
}

func TestClientRealtimeOnlyFailsWhenOffline(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})
	if _, err := store.Observe(t.Context(), runningObservation("fallback")); err != nil {
		t.Fatal(err)
	}

	c := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: filepath.Join(t.TempDir(), "nonexistent.sock"),
		Mode:       client.ModeRealtimeOnly,
	})

	if c.Mode() != client.ModeRealtimeOnly {
		t.Fatalf("Mode() = %q, want %q", c.Mode(), client.ModeRealtimeOnly)
	}
	if c.Realtime() == nil {
		t.Fatal("Realtime() returned nil")
	}

	_, err := c.List(t.Context(), registry.Filter{Presence: registry.PresenceLive})
	if !client.IsUnavailable(err) {
		t.Fatalf("List() err = %v, want ErrUnavailable", err)
	}
}

func TestClientDurableOnlyBypassesBroker(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(storePath, catalog.Rules{})
	if _, err := store.Observe(t.Context(), runningObservation("durable-session")); err != nil {
		t.Fatal(err)
	}

	c := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: filepath.Join(t.TempDir(), "nonexistent.sock"),
		Mode:       client.ModeDurableOnly,
	})

	if c.Mode() != client.ModeDurableOnly {
		t.Fatalf("Mode() = %q, want %q", c.Mode(), client.ModeDurableOnly)
	}

	sessions, err := c.List(t.Context(), registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		t.Fatalf("List() err = %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "durable-session" {
		t.Fatalf("List() = %#v, want durable-session", sessions)
	}

	_, err = c.Subscribe(t.Context(), registry.Filter{})
	if err == nil {
		t.Fatal("Subscribe() in ModeDurableOnly succeeded, want error")
	}
}

//nolint:cyclop // Verifies broker startup, streaming updates, and cancellation in one test.
func TestClientWatchYieldsRealtimeRevisions(t *testing.T) {
	t.Parallel()

	storePath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(storePath)
		_ = os.Remove(broker.SocketPath(storePath))
	})
	store, err := registry.OpenMemoryStore(storePath, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan struct{})
	serverErrors := make(chan error, 1)
	server := brokerserver.New(brokerserver.Options{
		Store:      store,
		SocketPath: broker.SocketPath(storePath),
		Ready:      ready,
	})
	go func() { serverErrors <- server.Serve(ctx) }()
	select {
	case <-ready:
	case err := <-serverErrors:
		t.Fatalf("broker exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not become ready")
	}

	watchClient := client.New(client.Config{StorePath: storePath})
	watchErrors := make(chan error, 1)
	snapshots := make(chan registry.StateSnapshot, 2)
	go func() {
		watchErrors <- watchClient.Watch(ctx, registry.Filter{}, func(snapshot registry.StateSnapshot) error {
			snapshots <- snapshot
			return nil
		})
	}()

	initial := receiveSnapshot(t, snapshots)
	if len(initial.Sessions) != 0 {
		t.Fatalf("initial snapshot = %#v, want no sessions", initial)
	}
	if _, err := watchClient.Observe(t.Context(), runningObservation("live")); err != nil {
		t.Fatal(err)
	}
	updated := receiveSnapshot(t, snapshots)
	if updated.Revision <= initial.Revision || len(updated.Sessions) != 1 {
		t.Fatalf("updated snapshot = %#v, want a newer revision with one session", updated)
	}

	cancel()
	if err := <-watchErrors; err != nil {
		t.Fatalf("Watch() after cancellation = %v, want nil", err)
	}
	if err := <-serverErrors; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve() after cancellation = %v", err)
	}
}

func receiveSnapshot(t *testing.T, snapshots <-chan registry.StateSnapshot) registry.StateSnapshot {
	t.Helper()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case snapshot := <-snapshots:
		return snapshot
	case <-timer.C:
		t.Fatal("timed out waiting for state snapshot")
		return registry.StateSnapshot{}
	}
}

func runningObservation(sessionID string) registry.Observation {
	presence := registry.PresenceLive
	activity := registry.ActivityRunning
	return registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: sessionID}, Evidence: &registry.Report{Claim: &presence, Activity: &activity}}
}

// shortStatePath keeps the derived broker socket below Darwin's Unix socket path limit.
func shortStatePath() (string, error) {
	stateFile, err := os.CreateTemp("", "aht-client-*.json")
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

func TestClientSummaryModesParity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	storePath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(storePath) })
	socketPath := broker.SocketPath(storePath)

	fileStore := registry.NewJournal(storePath, catalog.Rules{})
	running := registry.ActivityRunning

	obs1 := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-1"}, Evidence: &registry.Report{Claim: new(registry.PresenceLive), Activity: &running, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "alpha"}, Listing: &registry.Listing{ProjectRoot: "/proj/a"}}}
	obs2 := registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-2"}, Evidence: &registry.Report{Claim: new(registry.PresenceLive), Activity: &running, Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "beta"}, Listing: &registry.Listing{ProjectRoot: "/proj/b"}}}
	obs3 := registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "sess-3"}, Evidence: &registry.Report{Claim: new(registry.PresenceGone), Location: &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "alpha"}, Listing: &registry.Listing{ProjectRoot: "/proj/a"}}}

	if _, err := fileStore.ObserveBatch(ctx, []registry.Observation{obs1, obs2, obs3}); err != nil {
		t.Fatal(err)
	}

	memStore, err := registry.OpenMemoryStore(storePath, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}

	ready := make(chan struct{})
	server := brokerserver.New(brokerserver.Options{
		Store:      memStore,
		SocketPath: socketPath,
		Ready:      ready,
	})
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(ctx) }()
	<-ready

	clientDurable := client.New(client.Config{StorePath: storePath, Mode: client.ModeDurableOnly})
	clientRealtime := client.New(client.Config{StorePath: storePath, SocketPath: socketPath, Mode: client.ModeRealtimeOnly})
	clientAuto := client.New(client.Config{StorePath: storePath, SocketPath: socketPath, Mode: client.ModeAuto})

	for _, groupBy := range []registry.SummaryGroupBy{
		registry.SummaryGroupByMultiplexerSession,
		registry.SummaryGroupByProject,
		registry.SummaryGroupByHarness,
	} {
		opts := registry.SummaryOptions{GroupBy: groupBy}
		durableSum, err := clientDurable.SummaryWithOptions(ctx, registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("durable error for %s: %v", groupBy, err)
		}
		realtimeSum, err := clientRealtime.SummaryWithOptions(ctx, registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("realtime error for %s: %v", groupBy, err)
		}
		autoSum, err := clientAuto.SummaryWithOptions(ctx, registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("auto error for %s: %v", groupBy, err)
		}

		assertSummaryParityForGroup(t, groupBy, durableSum, realtimeSum, autoSum)
	}

	assertUnsupportedGroupByModes(t, ctx, clientDurable, clientRealtime, clientAuto)

	cancel()
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func assertSummaryParityForGroup(t *testing.T, groupBy registry.SummaryGroupBy, durableSum, realtimeSum, autoSum []registry.Summary) {
	t.Helper()
	if len(durableSum) != len(realtimeSum) || len(realtimeSum) != len(autoSum) {
		t.Fatalf("len mismatch for %s: durable=%d, realtime=%d, auto=%d",
			groupBy, len(durableSum), len(realtimeSum), len(autoSum))
	}

	for i := range durableSum {
		d, r, a := durableSum[i], realtimeSum[i], autoSum[i]
		if d.GroupKey != r.GroupKey || r.GroupKey != a.GroupKey {
			t.Fatalf("group %s key mismatch: durable=%q, realtime=%q, auto=%q", groupBy, d.GroupKey, r.GroupKey, a.GroupKey)
		}
		if d.Total != r.Total || r.Total != a.Total {
			t.Fatalf("group %s total mismatch: durable=%d, realtime=%d, auto=%d", groupBy, d.Total, r.Total, a.Total)
		}
		if d.Live != r.Live || r.Live != a.Live {
			t.Fatalf("group %s live mismatch: durable=%d, realtime=%d, auto=%d", groupBy, d.Live, r.Live, a.Live)
		}
		if d.Gone != r.Gone || r.Gone != a.Gone {
			t.Fatalf("group %s gone mismatch: durable=%d, realtime=%d, auto=%d", groupBy, d.Gone, r.Gone, a.Gone)
		}
	}
}

func assertUnsupportedGroupByModes(t *testing.T, ctx context.Context, d, r, a *client.Client) {
	t.Helper()
	badOpts := registry.SummaryOptions{GroupBy: "invalid"}
	if _, err := d.SummaryWithOptions(ctx, registry.Filter{}, badOpts); !errors.Is(err, client.ErrUnsupportedGroupBy) {
		t.Fatalf("durable error = %v, want ErrUnsupportedGroupBy", err)
	}
	if _, err := r.SummaryWithOptions(ctx, registry.Filter{}, badOpts); err == nil {
		t.Fatal("realtime error = nil, want error")
	}
	if _, err := a.SummaryWithOptions(ctx, registry.Filter{}, badOpts); err == nil {
		t.Fatal("auto error = nil, want error")
	}
}
