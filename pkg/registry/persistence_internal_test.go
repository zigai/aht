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
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	catalog := &CatalogMetadata{Current: false, CWD: "/history"}
	_, err := store.ObserveBatch(context.Background(), []Observation{{Source: ObservationSourceCatalog, Evidence: ObservationEvidenceCatalogMetadata, Harness: HarnessGoose, Identity: ObservationIdentity{SessionID: "old"}, Catalog: catalog, ObservedAt: at}})
	if err != nil {
		t.Fatal(err)
	}
	if sessions, listErr := store.List(context.Background(), Filter{}); listErr != nil || len(sessions) != 0 {
		t.Fatalf("historical catalog created a record: %v %#v", listErr, sessions)
	}
	catalog.Current = true
	session, err := store.Observe(context.Background(), Observation{Source: ObservationSourceCatalog, Evidence: ObservationEvidenceCatalogMetadata, Harness: HarnessClaude, Identity: ObservationIdentity{SessionID: "current"}, Catalog: catalog, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != PresenceUnknown || session.Activity == nil || *session.Activity != ActivityUnknown {
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
	store := NewFileStore(path)
	if _, err := store.List(t.Context(), Filter{}); !errors.Is(err, ErrStoreTooLarge) {
		t.Fatalf("list error = %v, want oversized error", err)
	}
	if _, err := OpenMemoryStore(path); !errors.Is(err, ErrStoreTooLarge) {
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
	store := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
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
	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"))
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
	store, err := OpenMemoryStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := range 32 {
		group.Go(func() {
			activity := ActivityIdle
			_, err := store.Observe(t.Context(), Observation{
				Harness: HarnessCodex, Source: ObservationSourceNative,
				Evidence: ObservationEvidenceNativeEvent, Identity: ObservationIdentity{SessionID: strconv.Itoa(index)}, Activity: &activity,
			})
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
	persisted, err := NewFileStore(store.Path()).load()
	if err != nil {
		t.Fatal(err)
	}
	if !materialSnapshotsEqual(persisted, store.snapshot) {
		t.Fatalf("persisted %d sessions, want latest %d sessions", len(persisted.Sessions), len(store.snapshot.Sessions))
	}
}
