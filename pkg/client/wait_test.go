package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/v2/internal/brokerserver"
	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/client"
	"github.com/zigai/aht/v2/pkg/registry"
)

func makeTestSession(id string, presence registry.Presence, activity registry.Activity) registry.Session {
	var act *registry.Activity
	if activity != "" {
		act = &activity
	}
	return registry.Session{
		SchemaVersion: 1,
		ID:            id,
		Harness:       registry.Harness("pi"),
		SessionID:     id,
		SessionPath:   "",
		ResumeCommand: nil,
		CWD:           "/tmp",
		ProjectRoot:   "/tmp",
		Process:       nil,
		Location:      registry.Location{},

		Observations:      registry.Observations{},
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		PresenceChangedAt: time.Now().UTC(),
		ActivityChangedAt: time.Now().UTC(),
		Liveness:          registry.NewLiveness(presence, registry.ActivityValue(act), nil),
	}
}

func TestWaitValidation(t *testing.T) {
	t.Parallel()

	c := client.New(client.Config{})

	tests := []struct {
		name    string
		options client.WaitOptions
		wantErr error
	}{
		{
			name:    "empty session ID",
			options: client.WaitOptions{ID: "", Activity: client.ActivityIdle},
			wantErr: client.ErrSessionRequired,
		},
		{
			name:    "whitespace session ID",
			options: client.WaitOptions{ID: "   ", Activity: client.ActivityIdle},
			wantErr: client.ErrSessionRequired,
		},
		{
			name:    "negative timeout",
			options: client.WaitOptions{ID: "sess-1", Activity: client.ActivityIdle, Timeout: -time.Second},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name:    "negative stable-for",
			options: client.WaitOptions{ID: "sess-1", Activity: client.ActivityIdle, StableFor: -time.Second},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name:    "stable-for exceeds timeout",
			options: client.WaitOptions{ID: "sess-1", Activity: client.ActivityIdle, Timeout: time.Second, StableFor: 2 * time.Second},
			wantErr: client.ErrInvalidDuration,
		},
		{
			name:    "empty condition",
			options: client.WaitOptions{ID: "sess-1"},
			wantErr: client.ErrConditionRequired,
		},
		{
			name:    "invalid presence",
			options: client.WaitOptions{ID: "sess-1", Presence: "bogus"},
			wantErr: registry.ErrUnknownPresence,
		},
		{
			name:    "invalid activity",
			options: client.WaitOptions{ID: "sess-1", Activity: "bogus"},
			wantErr: registry.ErrUnknownActivity,
		},
		{
			name:    "contradictory presence gone with activity",
			options: client.WaitOptions{ID: "sess-1", Presence: client.PresenceGone, Activity: client.ActivityIdle},
			wantErr: client.ErrContradictoryCondition,
		},
		{
			name:    "contradictory presence unknown with activity",
			options: client.WaitOptions{ID: "sess-1", Presence: client.PresenceUnknown, Activity: client.ActivityRunning},
			wantErr: client.ErrContradictoryCondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.Wait(context.Background(), tt.options)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Wait() err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestWaitInitiallySatisfied(t *testing.T) {
	t.Parallel()

	session := makeTestSession("sess-init", registry.PresenceLive, registry.ActivityIdle)
	tw := client.NewTestWatcher()
	tw.SendSessions(session)

	c := client.New(client.Config{})
	c.SetWatcherForTest(tw)

	res, err := c.Wait(context.Background(), client.WaitOptions{
		ID:       "sess-init",
		Activity: client.ActivityIdle,
	})
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !res.Initial {
		t.Errorf("res.Initial = %v, want true", res.Initial)
	}
	if res.Session.ID != "sess-init" {
		t.Errorf("res.Session.ID = %q, want sess-init", res.Session.ID)
	}
	if !tw.IsClosed() {
		t.Error("expected watcher to be closed after wait")
	}
}

func TestWaitInitiallySatisfiedWithStableFor(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		session := makeTestSession("sess-stable", registry.PresenceLive, registry.ActivityIdle)
		tw := client.NewTestWatcher()
		tw.SendSessions(session)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		var res client.WaitResult
		var waitErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			res, waitErr = c.Wait(context.Background(), client.WaitOptions{
				ID:        "sess-stable",
				Activity:  client.ActivityIdle,
				StableFor: 500 * time.Millisecond,
			})
		})

		synctest.Wait()
		wg.Wait()

		if waitErr != nil {
			t.Fatalf("Wait() error = %v", waitErr)
		}
		if res.Initial {
			t.Errorf("res.Initial = %v, want false with StableFor", res.Initial)
		}
		if res.Session.ID != "sess-stable" {
			t.Errorf("res.Session.ID = %q, want sess-stable", res.Session.ID)
		}
		if !tw.IsClosed() {
			t.Error("expected watcher to be closed")
		}
	})
}

func TestWaitEventualTransition(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initial := makeTestSession("sess-trans", registry.PresenceLive, registry.ActivityRunning)
		tw := client.NewTestWatcher()
		tw.SendSessions(initial)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		var res client.WaitResult
		var waitErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			res, waitErr = c.Wait(context.Background(), client.WaitOptions{
				ID:       "sess-trans",
				Activity: client.ActivityIdle,
			})
		})
		// Allow initial snapshot to be processed
		synctest.Wait()

		// Send transition to idle
		updated := makeTestSession("sess-trans", registry.PresenceLive, registry.ActivityIdle)
		tw.SendSessions(updated)

		synctest.Wait()
		wg.Wait()

		if waitErr != nil {
			t.Fatalf("Wait() error = %v", waitErr)
		}
		if res.Initial {
			t.Errorf("res.Initial = %v, want false for transition", res.Initial)
		}
		if res.Session.Activity() == nil || *res.Session.Activity() != registry.ActivityIdle {
			t.Errorf("res.Session.Activity = %v, want idle", res.Session.Activity())
		}
	})
}

func TestWaitRapidFlickerAndStableForReset(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		running := makeTestSession("sess-flicker", registry.PresenceLive, registry.ActivityRunning)
		idle := makeTestSession("sess-flicker", registry.PresenceLive, registry.ActivityIdle)

		tw := client.NewTestWatcher()
		tw.SendSessions(running)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		type waitOutcome struct {
			res client.WaitResult
			err error
		}
		done := make(chan waitOutcome, 1)
		go func() {
			res, err := c.Wait(context.Background(), client.WaitOptions{
				ID:        "sess-flicker",
				Activity:  client.ActivityIdle,
				StableFor: 300 * time.Millisecond,
			})
			done <- waitOutcome{res: res, err: err}
		}()

		synctest.Wait()

		// Transition to idle
		tw.SendSessions(idle)
		synctest.Wait()

		// Advance time partially (100ms) - condition holds but stable duration not reached yet
		time.Sleep(100 * time.Millisecond)

		// Flicker back to running - resets stable-for timer!
		tw.SendSessions(running)
		synctest.Wait()

		// Sleep past the original 300ms deadline; wait must NOT complete because it flickered!
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		select {
		case out := <-done:
			t.Fatalf("Wait completed prematurely after flicker at original deadline: %+v", out)
		default:
		}

		// Now transition back to idle and let it stay stable for full 300ms
		idleStart := time.Now()
		tw.SendSessions(idle)
		synctest.Wait()

		// Advance to just before the 300ms stability deadline
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		select {
		case out := <-done:
			t.Fatalf("Wait completed before full stability interval elapsed: %+v", out)
		default:
		}

		// Sleep the remaining 50ms to reach full 300ms uninterrupted stability
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		select {
		case out := <-done:
			if out.err != nil {
				t.Fatalf("Wait() error = %v", out.err)
			}
			if out.res.Session.ID != "sess-flicker" {
				t.Errorf("res.Session.ID = %q, want sess-flicker", out.res.Session.ID)
			}
			if out.res.Session.Activity() == nil || *out.res.Session.Activity() != registry.ActivityIdle {
				t.Errorf("res.Session.Activity = %v, want idle", out.res.Session.Activity())
			}
			if elapsed := time.Since(idleStart); elapsed < 300*time.Millisecond {
				t.Errorf("virtual elapsed time since second idle = %v, want >= 300ms", elapsed)
			}
		default:
			t.Fatal("Wait did not complete after full stability duration")
		}
	})
}

func TestWaitTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		running := makeTestSession("sess-timeout", registry.PresenceLive, registry.ActivityRunning)
		tw := client.NewTestWatcher()
		tw.SendSessions(running)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		var waitErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			_, waitErr = c.Wait(context.Background(), client.WaitOptions{
				ID:       "sess-timeout",
				Activity: client.ActivityIdle,
				Timeout:  200 * time.Millisecond,
			})
		})

		synctest.Wait()
		wg.Wait()

		if !errors.Is(waitErr, client.ErrWaitTimeout) {
			t.Fatalf("Wait() error = %v, want ErrWaitTimeout", waitErr)
		}
		if !tw.IsClosed() {
			t.Error("expected watcher to be closed after timeout")
		}
	})
}

func TestWaitCallerCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		running := makeTestSession("sess-cancel", registry.PresenceLive, registry.ActivityRunning)
		tw := client.NewTestWatcher()
		tw.SendSessions(running)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		ctx, cancel := context.WithCancel(context.Background())
		var waitErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			_, waitErr = c.Wait(ctx, client.WaitOptions{
				ID:       "sess-cancel",
				Activity: client.ActivityIdle,
			})
		})
		synctest.Wait()
		cancel()
		synctest.Wait()
		wg.Wait()

		if !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("Wait() error = %v, want context.Canceled", waitErr)
		}
		if !tw.IsClosed() {
			t.Error("expected watcher to be closed after cancellation")
		}
	})
}

func assertPresenceErrorOnWaitActivity(t *testing.T, sessionID string, presence registry.Presence, wantErr error) {
	t.Helper()
	t.Run("initially "+string(presence), func(t *testing.T) {
		t.Parallel()
		sess := makeTestSession(sessionID+"-init", presence, "")
		tw := client.NewTestWatcher()
		tw.SendSessions(sess)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		_, err := c.Wait(context.Background(), client.WaitOptions{
			ID:       sessionID + "-init",
			Activity: client.ActivityIdle,
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Wait() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("transitions to "+string(presence), func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			running := makeTestSession(sessionID+"-trans", registry.PresenceLive, registry.ActivityRunning)
			tw := client.NewTestWatcher()
			tw.SendSessions(running)

			c := client.New(client.Config{})
			c.SetWatcherForTest(tw)

			var waitErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				_, waitErr = c.Wait(context.Background(), client.WaitOptions{
					ID:       sessionID + "-trans",
					Activity: client.ActivityIdle,
				})
			})
			synctest.Wait()

			next := makeTestSession(sessionID+"-trans", presence, "")
			tw.SendSessions(next)

			synctest.Wait()
			wg.Wait()

			if !errors.Is(waitErr, wantErr) {
				t.Fatalf("Wait() error = %v, want %v", waitErr, wantErr)
			}
		})
	})
}

func TestWaitSessionDisappeared(t *testing.T) {
	t.Parallel()
	assertPresenceErrorOnWaitActivity(t, "sess-gone", registry.PresenceGone, client.ErrSessionDisappeared)
}

func TestWaitPresenceGoneSucceedsWhenRequested(t *testing.T) {
	t.Parallel()

	t.Run("initially gone", func(t *testing.T) {
		t.Parallel()
		gone := makeTestSession("sess-want-gone-1", registry.PresenceGone, "")
		tw := client.NewTestWatcher()
		tw.SendSessions(gone)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		res, err := c.Wait(context.Background(), client.WaitOptions{
			ID:       "sess-want-gone-1",
			Presence: client.PresenceGone,
		})
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if !res.Initial {
			t.Errorf("res.Initial = %v, want true", res.Initial)
		}
		if res.Session.Presence() != registry.PresenceGone {
			t.Errorf("res.Session.Presence = %v, want gone", res.Session.Presence())
		}
	})

	t.Run("transitions to gone", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			live := makeTestSession("sess-want-gone-2", registry.PresenceLive, registry.ActivityRunning)
			tw := client.NewTestWatcher()
			tw.SendSessions(live)

			c := client.New(client.Config{})
			c.SetWatcherForTest(tw)

			var res client.WaitResult
			var waitErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				res, waitErr = c.Wait(context.Background(), client.WaitOptions{
					ID:       "sess-want-gone-2",
					Presence: client.PresenceGone,
				})
			})
			synctest.Wait()

			gone := makeTestSession("sess-want-gone-2", registry.PresenceGone, "")
			tw.SendSessions(gone)

			synctest.Wait()
			wg.Wait()

			if waitErr != nil {
				t.Fatalf("Wait() error = %v", waitErr)
			}
			if res.Initial {
				t.Errorf("res.Initial = %v, want false for transition", res.Initial)
			}
			if res.Session.Presence() != registry.PresenceGone {
				t.Errorf("res.Session.Presence = %v, want gone", res.Session.Presence())
			}
		})
	})
}

func TestWaitUnknownState(t *testing.T) {
	t.Parallel()
	assertPresenceErrorOnWaitActivity(t, "sess-unk", registry.PresenceUnknown, client.ErrUnknownState)
}

func TestWaitSessionNotFoundAndDeleted(t *testing.T) {
	t.Parallel()

	t.Run("absent in initial snapshot", func(t *testing.T) {
		t.Parallel()
		other := makeTestSession("other-sess", registry.PresenceLive, registry.ActivityRunning)
		tw := client.NewTestWatcher()
		tw.SendSessions(other)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		_, err := c.Wait(context.Background(), client.WaitOptions{
			ID:       "missing-sess",
			Activity: client.ActivityIdle,
		})
		if !errors.Is(err, registry.ErrSessionNotFound) {
			t.Fatalf("Wait() error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("removed in subsequent snapshot", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			target := makeTestSession("target-sess", registry.PresenceLive, registry.ActivityRunning)
			tw := client.NewTestWatcher()
			tw.SendSessions(target)

			c := client.New(client.Config{})
			c.SetWatcherForTest(tw)

			var waitErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				_, waitErr = c.Wait(context.Background(), client.WaitOptions{
					ID:       "target-sess",
					Activity: client.ActivityIdle,
				})
			})
			synctest.Wait()

			// Session removed/deleted from snapshot
			tw.SendSessions()

			synctest.Wait()
			wg.Wait()

			if !errors.Is(waitErr, registry.ErrSessionNotFound) {
				t.Fatalf("Wait() error = %v, want ErrSessionNotFound", waitErr)
			}
		})
	})
}

func TestWaitBrokerDisconnect(t *testing.T) {
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

	observation := runningObservation("disconnect-session")
	accepted, err := store.Observe(t.Context(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	serverCtx, serverCancel := context.WithCancel(t.Context())
	ready := make(chan struct{})
	server := brokerserver.New(brokerserver.Options{
		Store:      store,
		SocketPath: broker.SocketPath(storePath),
		Ready:      ready,
	})

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Serve(serverCtx)
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for broker readiness")
	}

	c := client.New(client.Config{
		StorePath:  storePath,
		SocketPath: broker.SocketPath(storePath),
		Mode:       client.ModeRealtimeOnly,
	})

	waitErrCh := make(chan error, 1)
	go func() {
		_, waitErr := c.Wait(t.Context(), client.WaitOptions{
			ID:       accepted.ID,
			Activity: client.ActivityIdle,
		})
		waitErrCh <- waitErr
	}()

	// Give wait time to subscribe and observe initial snapshot
	time.Sleep(50 * time.Millisecond)

	// Disconnect broker
	serverCancel()

	select {
	case waitErr := <-waitErrCh:
		if !client.IsUnavailable(waitErr) {
			t.Fatalf("Wait() error after disconnect = %v, want IsUnavailable", waitErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Wait() to return after broker disconnect")
	}

	<-serverErrCh
}

func TestWaitDurableWatchBehavior(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	storePath := filepath.Join(directory, "sessions.json")
	fileStore := registry.NewJournal(storePath, catalog.Rules{})

	presence := registry.PresenceLive
	running := registry.ActivityRunning
	observed, err := fileStore.Observe(t.Context(), registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "durable-sess"}, Evidence: &registry.Report{Claim: &presence, Activity: &running}})
	if err != nil {
		t.Fatal(err)
	}

	c := client.New(client.Config{
		StorePath: storePath,
		Mode:      client.ModeDurableOnly,
	})

	resCh := make(chan client.WaitResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, waitErr := c.Wait(t.Context(), client.WaitOptions{
			ID:       observed.ID,
			Activity: client.ActivityIdle,
		})
		if waitErr != nil {
			errCh <- waitErr
			return
		}
		resCh <- res
	}()

	// Give file store watcher time to initialize and read baseline
	time.Sleep(100 * time.Millisecond)

	// Update session to idle on durable file store
	idle := registry.ActivityIdle
	_, err = fileStore.Observe(t.Context(), registry.Observation{Harness: registry.Harness("pi"), At: time.Now().UTC().Add(time.Second), Subject: registry.ObservationIdentity{SessionID: "durable-sess"}, Evidence: &registry.Report{Claim: &presence, Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-resCh:
		if res.Session.Activity() == nil || *res.Session.Activity() != registry.ActivityIdle {
			t.Fatalf("res.Session.Activity = %v, want idle", res.Session.Activity())
		}
	case waitErr := <-errCh:
		t.Fatalf("Wait() error = %v", waitErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for durable Wait() to complete")
	}
}

func TestWaitResourceCleanup(t *testing.T) {
	t.Parallel()

	t.Run("cleans up on success", func(t *testing.T) {
		t.Parallel()
		session := makeTestSession("sess-clean-1", registry.PresenceLive, registry.ActivityIdle)
		tw := client.NewTestWatcher()
		tw.SendSessions(session)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		_, err := c.Wait(context.Background(), client.WaitOptions{
			ID:       "sess-clean-1",
			Activity: client.ActivityIdle,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !tw.IsClosed() {
			t.Error("watcher was not closed on success")
		}
	})

	t.Run("cleans up on timeout", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			session := makeTestSession("sess-clean-2", registry.PresenceLive, registry.ActivityRunning)
			tw := client.NewTestWatcher()
			tw.SendSessions(session)

			c := client.New(client.Config{})
			c.SetWatcherForTest(tw)

			var wg sync.WaitGroup
			wg.Go(func() {
				_, _ = c.Wait(context.Background(), client.WaitOptions{
					ID:       "sess-clean-2",
					Activity: client.ActivityIdle,
					Timeout:  50 * time.Millisecond,
				})
			})

			synctest.Wait()
			wg.Wait()

			if !tw.IsClosed() {
				t.Error("watcher was not closed on timeout")
			}
		})
	})

	t.Run("cleans up on error", func(t *testing.T) {
		t.Parallel()
		session := makeTestSession("sess-clean-3", registry.PresenceGone, "")
		tw := client.NewTestWatcher()
		tw.SendSessions(session)

		c := client.New(client.Config{})
		c.SetWatcherForTest(tw)

		_, err := c.Wait(context.Background(), client.WaitOptions{
			ID:       "sess-clean-3",
			Activity: client.ActivityIdle,
		})
		if err == nil {
			t.Fatal("expected error")
		}
		if !tw.IsClosed() {
			t.Error("watcher was not closed on error")
		}
	})
}
