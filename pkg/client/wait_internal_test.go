package client

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestWaitTimeoutIncludesWatcherSetup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New(Config{})
		c.newWatcher = func(ctx context.Context, _ registry.Filter) (sessionWatcher, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		start := time.Now()
		_, err := c.Wait(t.Context(), WaitOptions{ID: "session", Activity: ActivityIdle, Timeout: time.Second})
		if !errors.Is(err, ErrWaitTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if time.Since(start) != time.Second {
			t.Fatalf("wait elapsed = %v", time.Since(start))
		}
	})
}

func TestWaitRestartsStabilityForNewIncarnation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New(Config{})
		watcher := NewTestWatcher()
		c.SetWatcherForTest(watcher)
		session := registry.Session{ID: "session", Presence: PresenceLive, Activity: new(ActivityIdle), Process: &registry.ProcessIdentity{PID: 10, StartIdentity: "first"}}
		watcher.SendSessions(session)
		start := time.Now()
		go func() {
			time.Sleep(500 * time.Millisecond)
			session.Process = &registry.ProcessIdentity{PID: 10, StartIdentity: "second"}
			watcher.SendSessions(session)
		}()
		result, err := c.Wait(t.Context(), WaitOptions{ID: "session", Activity: ActivityIdle, StableFor: time.Second, Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 1500*time.Millisecond || result.Session.Process.StartIdentity != "second" {
			t.Fatalf("returned early at %s: %+v", time.Since(start), result)
		}
	})
}

func TestWaitDurableStartupError(t *testing.T) {
	t.Parallel()
	c := New(Config{StorePath: filepath.Join(t.TempDir(), "missing", "state.json"), Mode: ModeDurableOnly})
	_, err := c.Wait(t.Context(), WaitOptions{ID: "missing", Activity: ActivityIdle, Timeout: time.Second})
	if err == nil || errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("startup error = %v", err)
	}
}
