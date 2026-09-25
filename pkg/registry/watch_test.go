package registry_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

const watchTestTimeout = 3 * time.Second

var errStopWatchCallback = errors.New("stop callback")

func TestFileStoreWatchInitialResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, behaviorRules{})
	want := observeWatchSession(t, store, "initial")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()

	var got registry.WatchResult
	err := store.Watch(ctx, registry.WatchOptions{}, func(result registry.WatchResult) error {
		got = result
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Err != nil || !got.Initial || got.UpdatedAt.IsZero() {
		t.Fatalf("initial result = %#v", got)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != want.ID {
		t.Fatalf("initial sessions = %#v, want %q", got.Sessions, want.ID)
	}
}

func TestFileStoreWatchUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, behaviorRules{})
	observeWatchSession(t, store, "first")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	updated := make(chan registry.WatchResult, 1)
	done := make(chan error, 1)
	go func() {
		done <- store.Watch(ctx, registry.WatchOptions{Debounce: time.Millisecond, ReconcileInterval: time.Hour}, func(result registry.WatchResult) error {
			if result.Err != nil {
				return result.Err
			}
			if result.Initial {
				close(initial)
				return nil
			}
			updated <- result
			cancel()
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	observeWatchSession(t, store, "second")
	result := awaitWatchResult(t, updated)
	if result.Initial || len(result.Sessions) != 2 {
		t.Fatalf("update result = %#v", result)
	}
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchAtomicRename(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "sessions.json")
	target := registry.NewJournal(targetPath, behaviorRules{})
	sourcePath := filepath.Join(directory, "replacement.json")
	source := registry.NewJournal(sourcePath, behaviorRules{})
	want := observeWatchSession(t, source, "replacement")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	updated := make(chan registry.WatchResult, 1)
	done := make(chan error, 1)
	go func() {
		done <- target.Watch(ctx, registry.WatchOptions{Debounce: time.Millisecond, ReconcileInterval: time.Hour}, func(result registry.WatchResult) error {
			if result.Err != nil {
				return result.Err
			}
			if result.Initial {
				close(initial)
				return nil
			}
			updated <- result
			cancel()
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	if err := os.Rename(sourcePath, targetPath); err != nil {
		t.Fatal(err)
	}
	result := awaitWatchResult(t, updated)
	if len(result.Sessions) != 1 || result.Sessions[0].ID != want.ID {
		t.Fatalf("atomic replacement result = %#v", result)
	}
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchReattachesAfterDirectoryRecreation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "state")
	targetPath := filepath.Join(directory, "sessions.json")
	target := registry.NewJournal(targetPath, behaviorRules{})
	observeWatchSession(t, target, "initial")
	source := registry.NewJournal(filepath.Join(root, "replacement.json"), behaviorRules{})
	replacement := observeWatchSession(t, source, "replacement")
	replacementData, err := os.ReadFile(source.Path())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	results := make(chan registry.WatchResult, 8)
	done := make(chan error, 1)
	go func() {
		done <- target.Watch(ctx, registry.WatchOptions{
			Debounce:          time.Millisecond,
			ReconcileInterval: time.Hour,
		}, func(result registry.WatchResult) error {
			if result.Err != nil {
				return result.Err
			}
			if result.Initial {
				close(initial)
				return nil
			}
			results <- result
			return nil
		})
	}()
	awaitWatchSignal(t, initial)

	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeWatchTestFile(t, targetPath, replacementData)
	awaitWatchSessionID(t, results, replacement.ID)

	added := observeWatchSession(t, target, "after-recreation")
	result := awaitWatchSessionID(t, results, added.ID)
	if len(result.Sessions) != 2 {
		t.Fatalf("post-recreation result = %#v", result)
	}
	cancel()
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchCancellation(t *testing.T) {
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx, cancel := context.WithCancel(context.Background())
	initial := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.Watch(ctx, registry.WatchOptions{}, func(result registry.WatchResult) error {
			if result.Initial {
				close(initial)
			}
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	cancel()
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchReconcilesMissedEvent(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "sessions.json")
	target := registry.NewJournal(targetPath, behaviorRules{})
	source := registry.NewJournal(filepath.Join(directory, "source.json"), behaviorRules{})
	want := observeWatchSession(t, source, "reconciled")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	updated := make(chan registry.WatchResult, 1)
	done := make(chan error, 1)
	go func() {
		done <- target.Watch(ctx, registry.WatchOptions{Debounce: time.Hour, ReconcileInterval: 10 * time.Millisecond}, func(result registry.WatchResult) error {
			if result.Err != nil {
				return result.Err
			}
			if result.Initial {
				close(initial)
				return nil
			}
			updated <- result
			cancel()
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	data, err := os.ReadFile(source.Path())
	if err != nil {
		t.Fatal(err)
	}
	writeWatchTestFile(t, targetPath, data)
	result := awaitWatchResult(t, updated)
	if len(result.Sessions) != 1 || result.Sessions[0].ID != want.ID {
		t.Fatalf("reconciled result = %#v", result)
	}
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchReportsReadErrorAndRetainsBaseline(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "sessions.json")
	target := registry.NewJournal(targetPath, behaviorRules{})
	wantBaseline := observeWatchSession(t, target, "baseline")
	source := registry.NewJournal(filepath.Join(directory, "recovered.json"), behaviorRules{})
	wantRecovered := observeWatchSession(t, source, "recovered")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	readError := make(chan registry.WatchResult, 1)
	recovered := make(chan registry.WatchResult, 1)
	done := make(chan error, 1)
	go func() {
		done <- target.Watch(ctx, registry.WatchOptions{Debounce: time.Millisecond, ReconcileInterval: time.Hour}, func(result registry.WatchResult) error {
			switch {
			case result.Initial:
				close(initial)
			case result.Err != nil:
				readError <- result
			default:
				recovered <- result
				cancel()
			}
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	replaceWatchTestFile(t, targetPath, []byte("{"))
	errorResult := awaitWatchResult(t, readError)
	if errorResult.Err == nil || len(errorResult.Sessions) != 1 || errorResult.Sessions[0].ID != wantBaseline.ID {
		t.Fatalf("read error result = %#v", errorResult)
	}
	if err := os.Rename(source.Path(), targetPath); err != nil {
		t.Fatal(err)
	}
	recoveredResult := awaitWatchResult(t, recovered)
	if len(recoveredResult.Sessions) != 1 || recoveredResult.Sessions[0].ID != wantRecovered.ID {
		t.Fatalf("recovered result = %#v", recoveredResult)
	}
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchRecoversInitialMalformedSnapshot(t *testing.T) {
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "sessions.json")
	writeWatchTestFile(t, targetPath, []byte("{"))
	target := registry.NewJournal(targetPath, behaviorRules{})
	source := registry.NewJournal(filepath.Join(directory, "recovered.json"), behaviorRules{})
	want := observeWatchSession(t, source, "recovered")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	readError := make(chan registry.WatchResult, 1)
	recovered := make(chan registry.WatchResult, 1)
	done := make(chan error, 1)

	go func() {
		done <- target.Watch(ctx, registry.WatchOptions{
			Debounce:          time.Millisecond,
			ReconcileInterval: time.Hour,
		}, func(result registry.WatchResult) error {
			if result.Err != nil {
				readError <- result
			} else {
				recovered <- result
				cancel()
			}
			return nil
		})
	}()
	errorResult := awaitWatchResult(t, readError)
	if errorResult.Initial || len(errorResult.Sessions) != 0 {
		t.Fatalf("initial read error result = %#v", errorResult)
	}
	if err := os.Rename(source.Path(), targetPath); err != nil {
		t.Fatal(err)
	}
	recoveredResult := awaitWatchResult(t, recovered)
	if !recoveredResult.Initial || len(recoveredResult.Sessions) != 1 || recoveredResult.Sessions[0].ID != want.ID {
		t.Fatalf("recovered initial result = %#v", recoveredResult)
	}
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchSerializesCallbacks(t *testing.T) {
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	observeWatchSession(t, store, "initial")
	ctx, cancel := context.WithTimeout(context.Background(), watchTestTimeout)
	defer cancel()
	initial := make(chan struct{})
	firstUpdate := make(chan struct{})
	releaseFirstUpdate := make(chan struct{})
	secondUpdate := make(chan struct{})
	concurrent := make(chan struct{}, 1)
	done := make(chan error, 1)
	var active atomic.Bool
	var updates atomic.Int32

	go func() {
		done <- store.Watch(ctx, registry.WatchOptions{
			Debounce:          time.Millisecond,
			ReconcileInterval: time.Hour,
		}, func(result registry.WatchResult) error {
			if !active.CompareAndSwap(false, true) {
				select {
				case concurrent <- struct{}{}:
				default:
				}
			}
			defer active.Store(false)
			if result.Err != nil {
				return result.Err
			}
			if result.Initial {
				close(initial)
				return nil
			}
			if updates.Add(1) == 1 {
				close(firstUpdate)
				<-releaseFirstUpdate
				return nil
			}
			close(secondUpdate)
			cancel()
			return nil
		})
	}()
	awaitWatchSignal(t, initial)
	observeWatchSession(t, store, "first-update")
	awaitWatchSignal(t, firstUpdate)
	observeWatchSession(t, store, "second-update")
	select {
	case <-concurrent:
		t.Fatal("watch callbacks overlapped")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirstUpdate)
	awaitWatchSignal(t, secondUpdate)
	if err := awaitWatchError(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreWatchReturnsCallbackError(t *testing.T) {
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	want := errStopWatchCallback
	err := store.Watch(context.Background(), registry.WatchOptions{}, func(registry.WatchResult) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Watch error = %v, want %v", err, want)
	}
}

func observeWatchSession(t *testing.T, store *registry.Journal, id string) registry.Session {
	t.Helper()
	activity := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: id}, Evidence: &registry.Report{Activity: &activity}})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func writeWatchTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	//nolint:gosec // callers construct paths beneath t.TempDir
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func replaceWatchTestFile(t *testing.T, targetPath string, data []byte) {
	t.Helper()
	temporary := filepath.Join(filepath.Dir(targetPath), "watch-replacement.tmp")
	writeWatchTestFile(t, temporary, data)
	if err := os.Rename(temporary, targetPath); err != nil {
		t.Fatal(err)
	}
}

func awaitWatchSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(watchTestTimeout):
		t.Fatal("timed out waiting for watch signal")
	}
}

func awaitWatchResult(t *testing.T, results <-chan registry.WatchResult) registry.WatchResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(watchTestTimeout):
		t.Fatal("timed out waiting for watch result")
		return registry.WatchResult{}
	}
}

func awaitWatchSessionID(t *testing.T, results <-chan registry.WatchResult, id string) registry.WatchResult {
	t.Helper()
	for {
		result := awaitWatchResult(t, results)
		for _, session := range result.Sessions {
			if session.ID == id {
				return result
			}
		}
	}
}

func awaitWatchError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(watchTestTimeout):
		t.Fatal("timed out waiting for watcher to stop")
		return nil
	}
}
