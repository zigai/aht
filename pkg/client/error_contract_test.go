package client_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/brokerserver"
	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestRegistryErrorClassificationAcrossModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []client.Mode{client.ModeDurableOnly, client.ModeRealtimeOnly} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			config, accepted, observation, _ := errorContractBroker(t)
			config.Mode = mode
			assertRegistryFailures(t, client.New(config), accepted, observation, mode == client.ModeRealtimeOnly)
		})
	}
}

func TestAutoRegistryErrorsSurviveBrokerShutdown(t *testing.T) {
	t.Parallel()

	config, accepted, observation, stop := errorContractBroker(t)
	config.Mode = client.ModeAuto
	c := client.New(config)
	assertRegistryFailures(t, c, accepted, observation, true)
	stop()
	assertRegistryFailures(t, c, accepted, observation, false)
}

func errorContractBroker(t *testing.T) (client.Config, registry.Session, registry.Observation, func()) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "sessions.json")
	socketPath, err := shortStatePath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := registry.OpenMemoryStore(storePath, catalog.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	observation := runningObservation("error-contract")
	accepted, err := store.Observe(t.Context(), observation)
	if err != nil {
		t.Fatal(err)
	}
	endProcessOnlySession(t, store)
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop := startErrorContractBroker(t, store, socketPath)
	return client.Config{StorePath: storePath, SocketPath: socketPath}, accepted, observation, stop
}

var endedProcess = registry.ProcessIdentity{PID: 4242, StartIdentity: "boot:4242", Executable: "/usr/bin/pi"}

// endProcessOnlySession records a process-only session and its exit, which
// removes the session and leaves its process recorded as ended.
func endProcessOnlySession(t *testing.T, store *registry.MemoryStore) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Second)
	for index, present := range []bool{true, false} {
		sighting := registry.Observation{Harness: registry.Harness("pi"), At: at.Add(time.Duration(index) * time.Millisecond), Evidence: &registry.Sighting{Process: endedProcess, Present: present}}
		if _, err := store.Observe(t.Context(), sighting); err != nil {
			t.Fatal(err)
		}
	}
}

func startErrorContractBroker(t *testing.T, store *registry.MemoryStore, socketPath string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan struct{})
	done := make(chan struct{})
	var serveErr error
	server := brokerserver.New(brokerserver.Options{Store: store, SocketPath: socketPath, Ready: ready})
	go func() {
		serveErr = server.Serve(ctx)
		close(done)
	}()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case <-done:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				t.Errorf("broker shutdown: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("broker did not stop")
		}
	})
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-done:
		t.Fatalf("broker exited before ready: %v", serveErr)
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not become ready")
	}
	return stop
}

func assertRegistryFailures(t *testing.T, c *client.Client, accepted registry.Session, observation registry.Observation, online bool) {
	t.Helper()
	_, err := c.Get(t.Context(), "missing")
	assertRegistryError(t, err, registry.ErrSessionNotFound, "not_found", online)

	idle := registry.ActivityIdle
	observation.SetActivity(&idle)
	observation.At = observation.At.Add(-time.Second)
	_, err = c.Observe(t.Context(), observation)
	assertRegistryError(t, err, registry.ErrObservationConflict, "observation_conflict", online)

	late := runningObservation("late-from-ended-process")
	process := endedProcess
	late.SetProcess(&process)
	_, err = c.Observe(t.Context(), late)
	assertRegistryError(t, err, registry.ErrProcessEnded, "process_ended", online)

	current, err := c.Get(t.Context(), accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, accepted) {
		t.Fatalf("rejected observation changed accepted state: got %#v, want %#v", current, accepted)
	}
}

func assertRegistryError(t *testing.T, err, sentinel error, code string, online bool) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, sentinel)
	}
	operationError, isOperation := errors.AsType[*client.OperationError](err)
	if isOperation != online {
		t.Fatalf("error = %T, broker operation error = %t, want %t", err, isOperation, online)
	}
	if !online {
		return
	}
	if operationError.Code != code {
		t.Fatalf("operation code = %q, want %q", operationError.Code, code)
	}
	remoteError, ok := errors.AsType[*broker.RemoteError](err)
	if !ok || !errors.Is(remoteError, sentinel) {
		t.Fatalf("broker error chain = %v, want %v", remoteError, sentinel)
	}
	if operationError.Message != remoteError.Message || operationError.Error() != remoteError.Error() {
		t.Fatalf("public error changed broker diagnostics: %v != %v", operationError, remoteError)
	}
}
