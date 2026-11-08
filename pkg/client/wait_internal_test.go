package client

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestWaitTimeoutIncludesWatcherSetup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New(Config{})
		c.realtime = &snapshotBroker{Client: broker.NewClient(""), snapshots: nil, closed: false}
		start := time.Now()

		_, err := c.Wait(
			t.Context(),
			WaitOptions{
				Reference: "session",
				Activity:  registry.ActivityIdle,
				Timeout:   time.Second,
			},
		)
		if !errors.Is(err, ErrWaitTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}

		if time.Since(start) != time.Second {
			t.Fatalf("wait elapsed = %v", time.Since(start))
		}
	})
}

type snapshotBroker struct {
	*broker.Client

	snapshots chan registry.StateSnapshot
	closed    bool
}

func newSnapshotBroker() *snapshotBroker {
	return &snapshotBroker{
		Client:    broker.NewClient(""),
		snapshots: make(chan registry.StateSnapshot, 16),
		closed:    false,
	}
}

func (b *snapshotBroker) Subscribe(
	ctx context.Context,
	_ registry.Filter,
) (*broker.Subscription, error) {
	if b.snapshots == nil {
		<-ctx.Done()
		return nil, fmt.Errorf("subscribe: %w", ctx.Err())
	}

	return broker.NewSubscription(b.snapshots, nil, func() { b.closed = true }), nil
}

func (b *snapshotBroker) SendSessions(sessions ...registry.Session) {
	b.snapshots <- registry.StateSnapshot{Sessions: sessions}
}

func (b *snapshotBroker) Closed() bool {
	return b.closed
}

func TestWaitRestartsStabilityForNewIncarnation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New(Config{})
		watcher := newSnapshotBroker()
		c.realtime = watcher

		session := registry.Session{
			ID:      "session",
			Process: &registry.ProcessIdentity{PID: 10, StartIdentity: "first"},
			Liveness: registry.NewLiveness(
				registry.PresenceLive,
				registry.ActivityValue(new(registry.ActivityIdle)),
				nil, time.Time{}, "",
			),
		}
		watcher.SendSessions(session)

		start := time.Now()

		go func() {
			time.Sleep(500 * time.Millisecond)

			session.Process = &registry.ProcessIdentity{PID: 10, StartIdentity: "second"}
			watcher.SendSessions(session)
		}()

		result, err := c.Wait(
			t.Context(),
			WaitOptions{
				Reference: "session",
				Activity:  registry.ActivityIdle,
				StableFor: time.Second,
				Timeout:   3 * time.Second,
			},
		)
		if err != nil {
			t.Fatal(err)
		}

		if time.Since(start) != 1500*time.Millisecond ||
			result.Session.Process.StartIdentity != "second" {
			t.Fatalf("returned early at %s: %+v", time.Since(start), result)
		}
	})
}

func TestWaitDurableStartupError(t *testing.T) {
	t.Parallel()
	c := New(
		Config{
			StorePath: filepath.Join(t.TempDir(), "missing", "state.json"),
			Mode:      ModeDurableOnly,
		},
	)

	_, err := c.Wait(
		t.Context(),
		WaitOptions{Reference: "missing", Activity: registry.ActivityIdle, Timeout: time.Second},
	)
	if err == nil || errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("startup error = %v", err)
	}
}

func makeTestSession(
	id string,
	presence registry.Presence,
	activity registry.Activity,
) registry.Session {
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
		Liveness: registry.NewLiveness(
			presence,
			registry.ActivityValue(act),
			nil,
			time.Time{},
			"",
		),
	}
}

func TestWaitInitiallySatisfied(t *testing.T) {
	t.Parallel()

	session := makeTestSession("sess-init", registry.PresenceLive, registry.ActivityIdle)
	tw := newSnapshotBroker()
	tw.SendSessions(session)

	c := New(Config{})
	c.realtime = tw

	res, err := c.Wait(context.Background(), WaitOptions{
		Reference: "sess-init",
		Activity:  registry.ActivityIdle,
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

	if !tw.Closed() {
		t.Error("expected watcher to be closed after wait")
	}
}

func TestWaitInitiallySatisfiedWithStableFor(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		session := makeTestSession("sess-stable", registry.PresenceLive, registry.ActivityIdle)
		tw := newSnapshotBroker()
		tw.SendSessions(session)

		c := New(Config{})
		c.realtime = tw

		var (
			res     WaitResult
			waitErr error
			wg      sync.WaitGroup
		)
		wg.Go(func() {
			res, waitErr = c.Wait(context.Background(), WaitOptions{
				Reference: "sess-stable",
				Activity:  registry.ActivityIdle,
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

		if !tw.Closed() {
			t.Error("expected watcher to be closed")
		}
	})
}

func TestWaitEventualTransition(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initial := makeTestSession("sess-trans", registry.PresenceLive, registry.ActivityRunning)
		tw := newSnapshotBroker()
		tw.SendSessions(initial)

		c := New(Config{})
		c.realtime = tw

		var (
			res     WaitResult
			waitErr error
			wg      sync.WaitGroup
		)
		wg.Go(func() {
			res, waitErr = c.Wait(context.Background(), WaitOptions{
				Reference: "sess-trans",
				Activity:  registry.ActivityIdle,
			})
		})
		// Allow initial snapshot to be processed
		synctest.Wait()

		// Send transition to idle
		idle := makeTestSession("sess-trans", registry.PresenceLive, registry.ActivityIdle)
		tw.SendSessions(idle)

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

		tw := newSnapshotBroker()
		tw.SendSessions(running)

		c := New(Config{})
		c.realtime = tw

		type waitOutcome struct {
			res WaitResult
			err error
		}

		done := make(chan waitOutcome, 1)

		go func() {
			res, err := c.Wait(context.Background(), WaitOptions{
				Reference: "sess-flicker",
				Activity:  registry.ActivityIdle,
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

			if out.res.Session.Activity() == nil ||
				*out.res.Session.Activity() != registry.ActivityIdle {
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
		tw := newSnapshotBroker()
		tw.SendSessions(running)

		c := New(Config{})
		c.realtime = tw

		var (
			waitErr error
			wg      sync.WaitGroup
		)
		wg.Go(func() {
			_, waitErr = c.Wait(context.Background(), WaitOptions{
				Reference: "sess-timeout",
				Activity:  registry.ActivityIdle,
				Timeout:   200 * time.Millisecond,
			})
		})

		synctest.Wait()
		wg.Wait()

		if !errors.Is(waitErr, ErrWaitTimeout) {
			t.Fatalf("Wait() error = %v, want ErrWaitTimeout", waitErr)
		}

		if !tw.Closed() {
			t.Error("expected watcher to be closed after timeout")
		}
	})
}

func TestWaitCallerCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		running := makeTestSession("sess-cancel", registry.PresenceLive, registry.ActivityRunning)
		tw := newSnapshotBroker()
		tw.SendSessions(running)

		c := New(Config{})
		c.realtime = tw

		ctx, cancel := context.WithCancel(context.Background())

		var (
			waitErr error
			wg      sync.WaitGroup
		)
		wg.Go(func() {
			_, waitErr = c.Wait(ctx, WaitOptions{
				Reference: "sess-cancel",
				Activity:  registry.ActivityIdle,
			})
		})
		synctest.Wait()
		cancel()
		synctest.Wait()
		wg.Wait()

		if !errors.Is(waitErr, context.Canceled) {
			t.Fatalf("Wait() error = %v, want context.Canceled", waitErr)
		}

		if !tw.Closed() {
			t.Error("expected watcher to be closed after cancellation")
		}
	})
}

func assertPresenceErrorOnWaitActivity(
	t *testing.T,
	sessionID string,
	presence registry.Presence,
	wantErr error,
) {
	t.Helper()
	t.Run("initially "+string(presence), func(t *testing.T) {
		t.Parallel()

		sess := makeTestSession(sessionID+"-init", presence, "")
		tw := newSnapshotBroker()
		tw.SendSessions(sess)

		c := New(Config{})
		c.realtime = tw

		_, err := c.Wait(context.Background(), WaitOptions{
			Reference: sessionID + "-init",
			Activity:  registry.ActivityIdle,
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Wait() error = %v, want %v", err, wantErr)
		}

		if !tw.Closed() {
			t.Fatal("subscription remains open after the failed wait")
		}
	})

	t.Run("transitions to "+string(presence), func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			running := makeTestSession(
				sessionID+"-trans",
				registry.PresenceLive,
				registry.ActivityRunning,
			)
			tw := newSnapshotBroker()
			tw.SendSessions(running)

			c := New(Config{})
			c.realtime = tw

			var (
				waitErr error
				wg      sync.WaitGroup
			)
			wg.Go(func() {
				_, waitErr = c.Wait(context.Background(), WaitOptions{
					Reference: sessionID + "-trans",
					Activity:  registry.ActivityIdle,
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

			if !tw.Closed() {
				t.Fatal("subscription remains open after the failed wait")
			}
		})
	})
}

func TestWaitSessionDisappeared(t *testing.T) {
	t.Parallel()
	assertPresenceErrorOnWaitActivity(
		t,
		"sess-gone",
		registry.PresenceGone,
		ErrSessionDisappeared,
	)
}

func TestWaitPresenceGoneSucceedsWhenRequested(t *testing.T) {
	t.Parallel()

	t.Run("initially gone", func(t *testing.T) {
		t.Parallel()

		gone := makeTestSession("sess-want-gone-1", registry.PresenceGone, "")
		tw := newSnapshotBroker()
		tw.SendSessions(gone)

		c := New(Config{})
		c.realtime = tw

		res, err := c.Wait(context.Background(), WaitOptions{
			Reference: "sess-want-gone-1",
			Presence:  registry.PresenceGone,
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
			live := makeTestSession(
				"sess-want-gone-2",
				registry.PresenceLive,
				registry.ActivityRunning,
			)
			tw := newSnapshotBroker()
			tw.SendSessions(live)

			c := New(Config{})
			c.realtime = tw

			var (
				res     WaitResult
				waitErr error
				wg      sync.WaitGroup
			)
			wg.Go(func() {
				res, waitErr = c.Wait(context.Background(), WaitOptions{
					Reference: "sess-want-gone-2",
					Presence:  registry.PresenceGone,
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
	assertPresenceErrorOnWaitActivity(
		t,
		"sess-unk",
		registry.PresenceUnknown,
		ErrUnknownState,
	)
}

func TestWaitSessionNotFoundAndDeleted(t *testing.T) {
	t.Parallel()

	t.Run("absent in initial snapshot", func(t *testing.T) {
		t.Parallel()

		other := makeTestSession("other-sess", registry.PresenceLive, registry.ActivityRunning)
		tw := newSnapshotBroker()
		tw.SendSessions(other)

		c := New(Config{})
		c.realtime = tw

		_, err := c.Wait(context.Background(), WaitOptions{
			Reference: "missing-sess",
			Activity:  registry.ActivityIdle,
		})
		if !errors.Is(err, registry.ErrSessionNotFound) {
			t.Fatalf("Wait() error = %v, want ErrSessionNotFound", err)
		}
	})

	t.Run("removed in subsequent snapshot", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			target := makeTestSession(
				"target-sess",
				registry.PresenceLive,
				registry.ActivityRunning,
			)
			tw := newSnapshotBroker()
			tw.SendSessions(target)

			c := New(Config{})
			c.realtime = tw

			var (
				waitErr error
				wg      sync.WaitGroup
			)
			wg.Go(func() {
				_, waitErr = c.Wait(context.Background(), WaitOptions{
					Reference: "target-sess",
					Activity:  registry.ActivityIdle,
				})
			})
			synctest.Wait()

			// Session removed/deleted from snapshot
			tw.SendSessions()

			synctest.Wait()
			wg.Wait()

			if !errors.Is(waitErr, ErrSessionDisappeared) {
				t.Fatalf("Wait() error = %v, want ErrSessionDisappeared", waitErr)
			}
		})
	})
}

func TestWaitPresenceGoneSucceedsWhenSessionIsRemoved(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		stableFor time.Duration
		sequence  [][]registry.Session
	}{
		{name: "removed while live", sequence: [][]registry.Session{nil}},
		{
			name: "gone then removed",
			sequence: [][]registry.Session{
				{
					makeTestSession(
						"removed-sess",
						registry.PresenceGone,
						"",
					),
				},
				nil,
			},
		},
		{name: "removed with stable-for", stableFor: time.Second, sequence: [][]registry.Session{nil, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				tw := newSnapshotBroker()
				tw.SendSessions(
					makeTestSession(
						"removed-sess",
						registry.PresenceLive,
						registry.ActivityRunning,
					),
				)

				c := New(Config{})
				c.realtime = tw

				var (
					res     WaitResult
					waitErr error
					wg      sync.WaitGroup
				)
				wg.Go(func() {
					res, waitErr = c.Wait(
						context.Background(),
						WaitOptions{
							Reference: "removed-sess",
							Presence:  registry.PresenceGone,
							StableFor: tc.stableFor,
						},
					)
				})
				synctest.Wait()

				for _, sessions := range tc.sequence {
					tw.SendSessions(sessions...)
					synctest.Wait()
				}

				wg.Wait()

				if waitErr != nil {
					t.Fatalf("Wait() error = %v, want removal to satisfy gone", waitErr)
				}

				if res.Initial || res.Session.ID != "removed-sess" ||
					res.Session.Presence() != registry.PresenceGone {
					t.Fatalf("Wait() result = %#v, want non-initial gone session", res)
				}
			})
		})
	}
}
