package registry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestFileStoreWatchSuppressesUnchangedSnapshots(t *testing.T) {
	store := NewJournal(filepath.Join(t.TempDir(), "sessions.json"), fixtureRules{})
	watcher := newScanTestWatcher(t)
	var results []WatchResult
	watch := fileStoreWatch{
		store: store.FileStore, watcher: watcher, directory: filepath.Dir(store.Path()),
		yield: func(result WatchResult) error {
			results = append(results, result)
			return result.Err
		},
	}
	if err := watch.scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap := newSnapshot()
	snap.UpdatedAt = time.Now().UTC()
	if err := writeSnapshotAtomic(store.Path(), snap); err != nil {
		t.Fatal(err)
	}
	// Drive the scan synchronously and assert that the replacement was actually
	// consumed. Filesystem event delivery is exercised by the public watch tests.
	if err := watch.scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !watch.baselineUpdatedAt.Equal(snap.UpdatedAt) {
		t.Fatalf("replacement timestamp = %v, want %v", watch.baselineUpdatedAt, snap.UpdatedAt)
	}
	if len(results) != 1 || !results[0].Initial {
		t.Fatalf("unchanged results = %#v, want only the initial result", results)
	}
	activity := ActivityIdle
	if _, err := store.Observe(t.Context(), Observation{Harness: HarnessCodex, At: time.Time{}, Subject: ObservationIdentity{SessionID: "changed"}, Evidence: &Report{Activity: &activity}}); err != nil {
		t.Fatal(err)
	}
	if err := watch.scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[1].Initial || len(results[1].Sessions) != 1 {
		t.Fatalf("changed results = %#v, want a second noninitial session snapshot", results)
	}
}

func newScanTestWatcher(t *testing.T) *fsnotify.Watcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Error(err)
		}
	})
	return watcher
}
