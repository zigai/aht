package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestV2CatalogCreationPolicyAndJSON(t *testing.T) {
	t.Parallel()
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	catalog := &Listing{Current: false, CWD: "/history"}
	_, err := store.ObserveBatch(context.Background(), []Observation{{Harness: HarnessGoose, At: at, Subject: ObservationIdentity{SessionID: "old"}, Evidence: catalog}})
	if err != nil {
		t.Fatal(err)
	}
	if sessions, listErr := store.List(context.Background(), Filter{}); listErr != nil || len(sessions) != 0 {
		t.Fatalf("historical catalog created a record: %v %#v", listErr, sessions)
	}
	catalog.Current = true
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessClaude, At: at, Subject: ObservationIdentity{SessionID: "current"}, Evidence: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != PresenceUnknown || session.Activity() == nil || *session.Activity() != ActivityUnknown {
		t.Fatalf("catalog reduction: %#v", session)
	}
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["state"]; ok {
		t.Fatalf("legacy state in wire: %s", data)
	}
	if wire["schema_version"] != float64(storeSchemaVersion) {
		t.Fatalf("schema version: %s", data)
	}
}

func TestSnapshotReadRejectsOversizedFileAndResetRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSnapshotBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	store := NewJournal(path, fixtureRules{})
	if _, err := store.List(t.Context(), Filter{}); !errors.Is(err, ErrStoreTooLarge) {
		t.Fatalf("list error = %v, want oversized error", err)
	}
	if _, err := OpenMemoryStore(path, fixtureRules{}); !errors.Is(err, ErrStoreTooLarge) {
		t.Fatalf("open error = %v, want oversized error", err)
	}
	if _, err := store.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sessions, err := store.List(t.Context(), Filter{}); err != nil || len(sessions) != 0 {
		t.Fatalf("reset sessions = %#v, error = %v", sessions, err)
	}
}

func TestFileStoreCanceledMutationDoesNotCommit(t *testing.T) {
	store := NewJournal(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := store.withSnapshot(ctx, func(snap *snapshot) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mutation error = %v, want canceled", err)
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("store stat error = %v, want no file committed", err)
	}
}

func TestMemoryStoreFlushWaitIsCancellable(t *testing.T) {
	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	store.flush <- struct{}{}
	defer func() { <-store.flush }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("flush error = %v, want canceled", err)
	}
}

func TestMemoryStoreConcurrentFlushPersistsLatestState(t *testing.T) {
	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"), fixtureRules{})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := range 32 {
		group.Go(func() {
			activity := ActivityIdle
			_, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: time.Time{}, Subject: ObservationIdentity{SessionID: strconv.Itoa(index)}, Evidence: &Report{Activity: &activity}})
			if err != nil {
				t.Error(err)
				return
			}
			if err := store.Flush(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	persisted, err := NewJournal(store.Path(), fixtureRules{}).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stateChanges(State{Sessions: persisted.Sessions}, State{Sessions: store.snapshot.Sessions})) != 0 {
		t.Fatalf("persisted %d sessions, want latest %d sessions", len(persisted.Sessions), len(store.snapshot.Sessions))
	}
}
