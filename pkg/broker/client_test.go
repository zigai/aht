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
	"strings"
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

func TestClientEmptyListReturnsEmptySlice(t *testing.T) {
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

func TestClientEmptySummaryReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	client := responseServer(t, func(id string) broker.Response {
		return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "result"}
	})

	summaries, err := client.SummaryWithOptions(t.Context(), registry.Filter{}, registry.SummaryOptions{GroupBy: registry.SummaryGroupByProject})
	if err != nil {
		t.Fatalf("SummaryWithOptions error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("SummaryWithOptions len = %d, want 0", len(summaries))
	}
}

func TestBrokerStoreFallbackParity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	storePath := filepath.Join(dir, "sessions.json")
	fileStore := registry.NewFileStore(storePath)

	running := registry.ActivityRunning
	obs := registry.Observation{
		Source:     registry.ObservationSourceNative,
		Evidence:   registry.ObservationEvidenceNativeEvent,
		Harness:    registry.HarnessClaude,
		Identity:   registry.ObservationIdentity{SessionID: "sess-fallback"},
		Presence:   new(registry.PresenceLive),
		Activity:   &running,
		Catalog:    &registry.CatalogMetadata{ProjectRoot: "/fallback/project"},
		Tmux:       &registry.TmuxContext{SessionName: "fallback-tmux"},
		ObservedAt: time.Now().UTC(),
	}
	if _, err := fileStore.Observe(t.Context(), obs); err != nil {
		t.Fatal(err)
	}

	// Nonexistent socket forces fallback to durable file store
	socketPath := filepath.Join(dir, "nonexistent.sock")
	bStore := broker.NewStoreForSocket(storePath, socketPath)

	for _, groupBy := range []registry.SummaryGroupBy{
		registry.SummaryGroupByMultiplexerSession,
		registry.SummaryGroupByProject,
		registry.SummaryGroupByHarness,
	} {
		opts := registry.SummaryOptions{GroupBy: groupBy}
		bSum, err := bStore.SummaryWithOptions(t.Context(), registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("fallback summary error for %s: %v", groupBy, err)
		}
		fSum, err := fileStore.SummaryWithOptions(t.Context(), registry.Filter{}, opts)
		if err != nil {
			t.Fatalf("filestore summary error for %s: %v", groupBy, err)
		}
		if len(bSum) != len(fSum) {
			t.Fatalf("len mismatch: fallback=%d fileStore=%d", len(bSum), len(fSum))
		}
		for i := range bSum {
			if bSum[i].GroupKey != fSum[i].GroupKey || bSum[i].Total != fSum[i].Total {
				t.Fatalf("mismatch at %d: fallback=%+v file=%+v", i, bSum[i], fSum[i])
			}
		}
	}
}

func TestBrokerOldJSONRequestDefaultsToMultiplexer(t *testing.T) {
	t.Parallel()

	type legacySummaryRequest struct {
		Version        int                     `json:"version"`
		ID             string                  `json:"id"`
		Method         string                  `json:"method"`
		GroupBy        registry.SummaryGroupBy `json:"group_by"`
		SummaryOptions registry.SummaryOptions `json:"summary_options"`
	}

	legacyJSON := `{"version":0,"id":"req-legacy","method":"summary"}`
	var req legacySummaryRequest
	if err := json.NewDecoder(strings.NewReader(legacyJSON)).Decode(&req); err != nil {
		t.Fatal(err)
	}

	if req.GroupBy != "" {
		t.Fatalf("unmarshaled GroupBy = %q, want empty string", req.GroupBy)
	}
	if req.SummaryOptions.GroupBy != "" {
		t.Fatalf("unmarshaled SummaryOptions.GroupBy = %q, want empty string", req.SummaryOptions.GroupBy)
	}

	// In server execution, empty GroupBy defaults to SummaryGroupByMultiplexerSession
	opts := req.SummaryOptions
	if opts.GroupBy == "" {
		opts.GroupBy = registry.SummaryGroupByMultiplexerSession
	}
	if opts.GroupBy != registry.SummaryGroupByMultiplexerSession {
		t.Fatalf("defaulted opts.GroupBy = %q, want multiplexer-session", opts.GroupBy)
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

func TestClientAppliesNewFiltersToOlderBrokerSnapshots(t *testing.T) {
	t.Parallel()
	sessions := []registry.Session{
		{ID: "wanted", Harness: registry.HarnessCodex, ProjectRoot: "/project/one"},
		{ID: "excluded", Harness: registry.HarnessClaude, ProjectRoot: "/project/two"},
	}
	filter := registry.Filter{Project: "/project/one"}
	t.Run("list", func(t *testing.T) {
		t.Parallel()
		c := responseServer(t, func(id string) broker.Response {
			return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "result", Sessions: sessions}
		})
		got, err := c.List(t.Context(), filter)
		if err != nil || len(got) != 1 || got[0].ID != "wanted" {
			t.Fatalf("filtered list = %+v, err = %v", got, err)
		}
	})
	t.Run("subscribe", func(t *testing.T) {
		t.Parallel()
		c := responseServer(t, func(id string) broker.Response {
			return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "snapshot", Snapshot: &registry.StateSnapshot{Sessions: sessions}}
		})
		sub, err := c.Subscribe(t.Context(), filter)
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Close()
		got := <-sub.Snapshots
		if len(got.Sessions) != 1 || got.Sessions[0].ID != "wanted" {
			t.Fatalf("filtered snapshot = %+v", got)
		}
	})
	t.Run("summary", func(t *testing.T) {
		t.Parallel()
		c := responseServer(t, func(id string) broker.Response {
			return broker.Response{Version: broker.ProtocolVersion, ID: id, Type: "result", Sessions: sessions}
		})
		got, err := c.SummaryWithOptions(t.Context(), filter, registry.SummaryOptions{GroupBy: registry.SummaryGroupByHarness})
		if err != nil || len(got) != 1 || got[0].Harness != registry.HarnessCodex || got[0].Total != 1 {
			t.Fatalf("filtered summary = %+v, err = %v", got, err)
		}
	})
}
