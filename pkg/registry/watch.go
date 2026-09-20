package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultWatchDebounce          = 100 * time.Millisecond
	defaultWatchReconcileInterval = 30 * time.Second
)

var (
	errWatchYieldRequired      = errors.New("watch yield callback is required")
	errWatchParentNotDirectory = errors.New("store watch parent is not a directory")
)

// WatchOptions controls filtering and filesystem event coalescing. Non-positive
// durations use package defaults.
type WatchOptions struct {
	Filter            Filter
	Debounce          time.Duration
	ReconcileInterval time.Duration
}

// WatchResult is an authoritative filtered snapshot or a transient watch/read
// error. Error results retain the most recent successful snapshot, when one is
// available. Initial is true only for the first successful snapshot.
type WatchResult struct {
	Sessions  []Session
	UpdatedAt time.Time
	Initial   bool
	Err       error
}

type fileStoreWatch struct {
	store             *FileStore
	options           WatchOptions
	yield             func(WatchResult) error
	watcher           *fsnotify.Watcher
	target            string
	directory         string
	baseline          []Session
	baselineUpdatedAt time.Time
	haveBaseline      bool
}

type watchRunState struct {
	watch         *fileStoreWatch
	debounceTimer *time.Timer
	debounce      <-chan time.Time
	reconcile     <-chan time.Time
}

// Watch yields serialized snapshots until ctx is canceled or yield returns an
// error. It observes the store directory before the initial scan and keeps its
// parent watched so atomic replacement and directory recreation remain visible.
func (s *FileStore) Watch(ctx context.Context, options WatchOptions, yield func(WatchResult) error) error {
	if ctx.Err() != nil {
		return nil
	}
	if yield == nil {
		return errWatchYieldRequired
	}
	options = normalizeFileStoreWatchOptions(options)
	target, directory, err := fileStoreWatchTarget(s.path)
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating store watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()
	if err := addFileStoreWatchPaths(watcher, directory); err != nil {
		return err
	}
	watch := fileStoreWatch{
		store:             s,
		options:           options,
		yield:             yield,
		watcher:           watcher,
		target:            target,
		directory:         directory,
		baseline:          nil,
		baselineUpdatedAt: time.Time{},
		haveBaseline:      false,
	}
	if err := watch.scan(); err != nil {
		return err
	}
	return watch.run(ctx)
}

func (watch *fileStoreWatch) scan() error {
	if err := watch.ensureDirectoryWatch(); err != nil {
		return watch.yieldError(err)
	}
	sessions, updatedAt, err := watch.store.watchSnapshot(watch.options.Filter)
	if err != nil {
		return watch.yieldError(err)
	}
	initial := !watch.haveBaseline
	unchanged := watch.haveBaseline && watchSessionsEqual(watch.baseline, sessions)
	// The fresh file snapshot is private; only callback values need cloning.
	watch.baseline = sessions
	watch.baselineUpdatedAt = updatedAt
	watch.haveBaseline = true
	if unchanged {
		return nil
	}
	result := WatchResult{Sessions: cloneWatchSessions(sessions), UpdatedAt: updatedAt, Initial: initial, Err: nil}
	if err := watch.yield(result); err != nil {
		return fmt.Errorf("yielding watch snapshot: %w", err)
	}
	return nil
}

func (watch *fileStoreWatch) yieldError(watchErr error) error {
	result := WatchResult{
		Sessions:  cloneWatchSessions(watch.baseline),
		UpdatedAt: watch.baselineUpdatedAt,
		Initial:   false,
		Err:       watchErr,
	}
	if err := watch.yield(result); err != nil {
		return fmt.Errorf("yielding watch error: %w", err)
	}
	return nil
}

func (watch *fileStoreWatch) run(ctx context.Context) error {
	debounceTimer := time.NewTimer(time.Hour)
	debounceTimer.Stop()
	defer debounceTimer.Stop()
	reconcile := time.NewTicker(watch.options.ReconcileInterval)
	defer reconcile.Stop()

	state := watchRunState{watch: watch, debounceTimer: debounceTimer, debounce: nil, reconcile: reconcile.C}
	for {
		done, err := state.step(ctx)
		if err != nil || done {
			return err
		}
	}
}

func (state *watchRunState) step(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return true, nil
	case event, ok := <-state.watch.watcher.Events:
		if !ok {
			return true, nil
		}
		if isFileStoreWatchDirectoryRemoval(event, state.watch.directory) {
			if err := state.watch.watcher.Remove(state.watch.directory); err != nil &&
				!errors.Is(err, fsnotify.ErrNonExistentWatch) {
				return false, state.watch.handleWatcherError(err)
			}
		}
		if isFileStoreWatchEvent(event, state.watch.target, state.watch.directory) {
			state.debounceTimer.Reset(state.watch.options.Debounce)
			state.debounce = state.debounceTimer.C
		}
		return false, nil
	case <-state.debounce:
		state.debounce = nil
		return false, state.watch.scan()
	case <-state.reconcile:
		return false, state.watch.scan()
	case watchErr, ok := <-state.watch.watcher.Errors:
		if !ok {
			return true, nil
		}
		return false, state.watch.handleWatcherError(watchErr)
	}
}

func (watch *fileStoreWatch) handleWatcherError(watchErr error) error {
	if watchErr == nil {
		return nil
	}
	if err := watch.yieldError(watchErr); err != nil {
		return err
	}
	return watch.scan()
}

func normalizeFileStoreWatchOptions(options WatchOptions) WatchOptions {
	if options.Debounce <= 0 {
		options.Debounce = defaultWatchDebounce
	}
	if options.ReconcileInterval <= 0 {
		options.ReconcileInterval = defaultWatchReconcileInterval
	}
	return options
}

func addFileStoreWatchPaths(watcher *fsnotify.Watcher, directory string) error {
	root := filepath.Dir(directory)
	if err := watcher.Add(root); err != nil {
		return fmt.Errorf("watching store directory parent: %w", err)
	}
	if err := watcher.Add(directory); err != nil {
		return fmt.Errorf("watching store directory: %w", err)
	}
	return nil
}

func (watch *fileStoreWatch) ensureDirectoryWatch() error {
	info, err := os.Stat(watch.directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stating store watch directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s", errWatchParentNotDirectory, watch.directory)
	}
	if err := watch.watcher.Add(watch.directory); err != nil {
		return fmt.Errorf("watching store directory: %w", err)
	}
	return nil
}

func fileStoreWatchTarget(path string) (string, string, error) {
	target, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolving store watch target: %w", err)
	}
	directory := filepath.Dir(target)
	info, err := os.Stat(directory)
	if err != nil {
		return "", "", fmt.Errorf("stating store watch directory: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%w: %s", errWatchParentNotDirectory, directory)
	}
	return filepath.Clean(target), directory, nil
}

func isFileStoreWatchEvent(event fsnotify.Event, target, directory string) bool {
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	path, err := filepath.Abs(event.Name)
	if err != nil {
		return false
	}
	path = filepath.Clean(path)
	return path == target || path == directory
}

func isFileStoreWatchDirectoryRemoval(event fsnotify.Event, directory string) bool {
	if event.Op&(fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	path, err := filepath.Abs(event.Name)
	if err != nil {
		return false
	}
	return filepath.Clean(path) == directory
}

func watchSessionsEqual(left, right []Session) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !reflect.DeepEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func cloneWatchSessions(sessions []Session) []Session {
	if sessions == nil {
		return nil
	}
	cloned := make([]Session, len(sessions))
	for index := range sessions {
		cloned[index] = cloneSessionValue(sessions[index])
	}
	return cloned
}

func (s *FileStore) watchSnapshot(filter Filter) ([]Session, time.Time, error) {
	snap, err := s.load()
	if err != nil {
		return nil, time.Time{}, err
	}
	sessions := make([]Session, 0, len(snap.Sessions))
	for _, session := range snap.Sessions {
		session.SchemaVersion = storeSchemaVersion
		populateMultiplexerProjection(&session)
		sessions = append(sessions, session)
	}
	return FilterSessions(sessions, filter), snap.UpdatedAt, nil
}
