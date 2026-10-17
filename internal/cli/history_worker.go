package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

var errHistoryRefreshRunning = errors.New("history refresh already running")

type historySessionState struct {
	presence registry.Presence
	activity registry.Activity
}

func (app *application) runHistoryIndexer(ctx context.Context, store *registry.MemoryStore, quiet bool) error {
	return watchHistoryRefresh(ctx, store, func(refreshCtx context.Context) {
		if err := app.refreshHistoryProcess(refreshCtx); err != nil && refreshCtx.Err() == nil && !quiet {
			app.warnf("warning: history indexing: %s\n", sanitizeHumanText(err.Error()))
		}
	})
}

func watchHistoryRefresh(ctx context.Context, store *registry.MemoryStore, refresh func(context.Context)) error {
	state, err := store.State(ctx, registry.Filter{})
	if err != nil {
		return historyWatchError(ctx, err)
	}
	_, previous := historyTransitions(state.Sessions, nil)
	workerCtx, cancel := context.WithCancel(ctx)
	signals := make(chan struct{}, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-signals:
			}
			if workerCtx.Err() != nil {
				return
			}
			refresh(workerCtx)
		}
	})
	defer func() {
		cancel()
		workers.Wait()
	}()
	signals <- struct{}{}
	for {
		state, err = store.WaitForRevision(ctx, state.Revision, registry.Filter{})
		if err != nil {
			return historyWatchError(ctx, err)
		}
		var changed bool
		changed, previous = historyTransitions(state.Sessions, previous)
		if changed {
			select {
			case signals <- struct{}{}:
			default:
			}
		}
	}
}

func historyWatchError(ctx context.Context, err error) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		return fmt.Errorf("watch sessions for history indexing: %w", err)
	}
}

func historyTransitions(sessions []registry.Session, previous map[string]historySessionState) (bool, map[string]historySessionState) {
	current := make(map[string]historySessionState, len(sessions))
	changed := false
	for _, session := range sessions {
		state := historySessionState{presence: session.Presence(), activity: ""}
		if activity := session.Activity(); activity != nil {
			state.activity = *activity
		}
		current[session.ID] = state
		before := previous[session.ID]
		if state.presence == registry.PresenceGone && before.presence != registry.PresenceGone || state.presence != registry.PresenceGone && state.activity == registry.ActivityIdle && state != before {
			changed = true
		}
	}
	return changed, current
}

func refreshSkippedHistory(result history.Result, refresh func() error) {
	if result.IndexUnavailable {
		return
	}
	for _, source := range result.Refresh {
		if source.Skipped > 0 {
			// Cache warming must not change search output or its exit status.
			_ = refresh()
			return
		}
	}
}

func startHistoryRefresh(ctx context.Context, sources []history.Source) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find history refresh executable: %w", err)
	}
	args := []string{"--no-config", "--json", "manage", "history", "refresh"}
	for _, source := range sources {
		args = append(args, "--source", string(source.Harness)+"="+source.Path)
	}
	return startDetachedHistoryCommand(exec.CommandContext(context.WithoutCancel(ctx), executable, args...))
}

func startDetachedHistoryCommand(command *exec.Cmd) error {
	if err := detachHistoryCommand(command); err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open history refresh streams: %w", err)
	}
	defer func() { _ = null.Close() }()
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	if err = startHistoryCommand(command); err != nil {
		return err
	}
	if err = command.Process.Release(); err != nil {
		return fmt.Errorf("release detached history refresh: %w", err)
	}
	return nil
}

func (app *application) refreshHistoryProcess(ctx context.Context) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find history refresh executable: %w", err)
	}
	args := []string{"--no-config", "--json", "manage", "history", "refresh"}
	var stderr bytes.Buffer
	if err := runHistoryCommand(ctx, executable, args, io.Discard, &stderr); err != nil {
		return fmt.Errorf("refresh history: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func runHistoryCommand(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdout, command.Stderr = stdout, stderr
	if err := startHistoryCommand(command); err != nil {
		return err
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("wait for history refresh: %w", err)
	}
	return nil
}

func startHistoryCommand(command *exec.Cmd) error {
	started := make(chan struct{})
	var startErr error
	go func() {
		// Exiting while locked discards the lowered-priority thread.
		runtime.LockOSThread()
		defer close(started)
		startErr = setHistoryPriority()
		if startErr == nil {
			startErr = command.Start()
		}
	}()
	<-started
	if startErr != nil {
		return fmt.Errorf("start history refresh: %w", startErr)
	}
	return nil
}
