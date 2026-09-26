package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultWatcherBufferSize = 16
	defaultFileWatchDebounce = 10 * time.Millisecond
)

var (
	// ErrWaitTimeout means the wait operation timed out before the condition was satisfied.
	ErrWaitTimeout = errors.New("wait condition timed out")

	// ErrSessionDisappeared means the session departed or became gone before the requested condition was met.
	ErrSessionDisappeared = errors.New("session disappeared before wait condition was met")

	// ErrUnknownState means the session entered an indeterminate or unknown state.
	ErrUnknownState = errors.New("session state is unknown")

	// ErrConditionRequired means neither activity nor presence condition was specified.
	ErrConditionRequired = errors.New("wait condition is required: specify activity or presence")

	// ErrContradictoryCondition means contradictory wait conditions were specified.
	ErrContradictoryCondition = errors.New("contradictory wait conditions specified")

	// ErrInvalidDuration means a duration was negative or stable-for exceeded timeout.
	ErrInvalidDuration = errors.New("invalid wait duration")

	// ErrSessionRequired means a canonical session ID was not provided.
	ErrSessionRequired = errors.New("session ID is required")
)

// WaitOptions configures the session condition, timeout, and stability duration to wait for.
type WaitOptions struct {
	// ID is the canonical registry session ID to watch.
	ID string

	// Activity optionally specifies the target activity state.
	Activity Activity

	// Presence optionally specifies the target presence state.
	Presence Presence

	// Timeout is the maximum duration to wait for the condition to be satisfied.
	// A zero duration means no timeout (wait until context cancellation).
	Timeout time.Duration

	// StableFor specifies the duration the condition must hold continuously
	// before the wait is considered satisfied.
	StableFor time.Duration
}

// WaitResult describes the session state that satisfied the wait condition.
type WaitResult struct {
	// Session is the session that satisfied the wait condition.
	Session Session `json:"session"`

	// Initial reports whether the condition was already satisfied by the initial snapshot.
	Initial bool `json:"initial"`
}

type watchEvent struct {
	sessions []registry.Session
	err      error
}

type sessionWatcher interface {
	Events() <-chan watchEvent
	Close()
}

type brokerSessionWatcher struct {
	subscription *broker.Subscription
	cancel       context.CancelFunc
	events       chan watchEvent
	done         chan struct{}
}

type durableSessionWatcher struct {
	cancel context.CancelFunc
	events chan watchEvent
	done   chan struct{}
}

// Validate checks that WaitOptions has a session ID, valid durations, and at least one non-contradictory condition.
func (o WaitOptions) Validate() error {
	if strings.TrimSpace(o.ID) == "" {
		return ErrSessionRequired
	}
	if err := o.validateDurations(); err != nil {
		return err
	}
	return o.validateConditions()
}

func (o WaitOptions) validateDurations() error {
	if o.Timeout < 0 {
		return fmt.Errorf("%w: timeout cannot be negative", ErrInvalidDuration)
	}
	if o.StableFor < 0 {
		return fmt.Errorf("%w: stable-for cannot be negative", ErrInvalidDuration)
	}
	if o.Timeout > 0 && o.StableFor > o.Timeout {
		return fmt.Errorf("%w: stable-for duration cannot exceed timeout", ErrInvalidDuration)
	}
	return nil
}

func (o WaitOptions) validateConditions() error {
	if o.Activity == "" && o.Presence == "" {
		return ErrConditionRequired
	}
	if o.Presence != "" && !o.Presence.IsValid() {
		return fmt.Errorf("%w: %q", registry.ErrUnknownPresence, o.Presence)
	}
	if o.Activity != "" && !o.Activity.IsValid() {
		return fmt.Errorf("%w: %q", registry.ErrUnknownActivity, o.Activity)
	}
	if o.Presence == PresenceGone && o.Activity != "" {
		return fmt.Errorf("%w: presence %q cannot be combined with activity %q", ErrContradictoryCondition, o.Presence, o.Activity)
	}
	if o.Presence == PresenceUnknown && o.Activity != "" {
		return fmt.Errorf("%w: presence %q cannot be combined with activity %q", ErrContradictoryCondition, o.Presence, o.Activity)
	}
	return nil
}

func emptyFilter() registry.Filter {
	return registry.Filter{
		Harness:            "",
		Presence:           "",
		Activity:           "",
		MultiplexerSession: "",
		Project:            "", ProjectSubtree: false, CWD: "", MultiplexerKind: "", MultiplexerServer: "", MultiplexerPane: "",
	}
}

// Wait waits for a session condition to be met, optionally requiring the condition
// to hold across observed snapshots for options.StableFor before returning.
//
// The tracker removes a session from the registry once it no longer needs a
// tombstone: immediately for sessions known only by their process, and after
// the tombstone TTL for sessions with a native identity. A session that
// disappears after Wait has seen it satisfies a gone presence condition; the
// result is the last observed session with a Gone liveness whose reason is
// "session_removed". For any other condition it returns ErrSessionDisappeared.
// A session missing from the first snapshot returns registry.ErrSessionNotFound.
//
//nolint:cyclop,gocognit // Wait coordinates multiple lifecycle events: timers, context, stream, and condition matching.
func (c *Client) Wait(ctx context.Context, options WaitOptions) (WaitResult, error) {
	if c.configErr != nil {
		return WaitResult{}, c.configErr
	}
	if err := options.Validate(); err != nil {
		return WaitResult{}, err
	}

	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	stream, err := c.startWatcher(ctx)
	if err != nil {
		return WaitResult{}, waitError(err)
	}
	_, durable := stream.(*durableSessionWatcher)
	defer func() {
		if stream != nil {
			stream.Close()
		}
	}()

	var stableTimer *time.Timer
	var stableTimerCh <-chan time.Time
	var candidateSession *registry.Session
	var lastSeen *registry.Session
	seenInitial := false
	stableReady := false

	resetStableTimer := func() {
		stableReady = false
		stopTimer(stableTimer)
		stableTimer = nil
		stableTimerCh = nil
		candidateSession = nil
	}
	defer func() {
		stopTimer(stableTimer)
	}()

	for {
		if err := ctx.Err(); err != nil {
			return WaitResult{}, waitError(err)
		}
		var ev watchEvent
		var ok bool
		// Consume already queued observations before accepting an elapsed stability
		// timer. Otherwise select could return a stale match while a change waits.
		select {
		case ev, ok = <-stream.Events():
		default:
			if stableReady && candidateSession != nil {
				return WaitResult{Session: *candidateSession, Initial: false}, nil
			}
			select {
			case <-ctx.Done():
				return WaitResult{}, waitError(ctx.Err())
			case <-stableTimerCh:
				stableReady = true
				stableTimerCh = nil
				continue
			case ev, ok = <-stream.Events():
			}
		}
		if !ok {
			resetStableTimer()
			if c.mode == ModeAuto && !durable {
				stream.Close()
				stream = newDurableWatcher(ctx, c.storePath)
				durable = true
				continue
			}
			return WaitResult{}, ErrUnavailable
		}

		if ev.err != nil {
			resetStableTimer()
			if c.mode == ModeAuto && !durable && (IsUnavailable(ev.err) || broker.IsUnavailable(ev.err)) {
				stream.Close()
				stream = newDurableWatcher(ctx, c.storePath)
				durable = true
				continue
			}
			return WaitResult{}, waitError(publicError(ev.err))
		}

		isInitial := !seenInitial
		seenInitial = true

		var currentSession *registry.Session
		for i := range ev.sessions {
			if ev.sessions[i].ID == options.ID {
				currentSession = &ev.sessions[i]
				break
			}
		}

		if currentSession == nil {
			if lastSeen == nil {
				resetStableTimer()
				return WaitResult{}, registry.ErrSessionNotFound
			}
			removed := removedSession(*lastSeen, time.Now().UTC())
			currentSession = &removed
		}
		lastSeen = currentSession

		matched, outcomeErr := evaluateCondition(*currentSession, options)
		if outcomeErr != nil {
			resetStableTimer()
			return WaitResult{}, outcomeErr
		}

		if matched {
			if options.StableFor == 0 {
				return WaitResult{Session: *currentSession, Initial: isInitial}, nil
			}
			if candidateSession != nil && !sameWaitProcess(candidateSession.Process, currentSession.Process) {
				resetStableTimer()
			}
			candidateSession = currentSession
			if stableTimer == nil {
				stableTimer = time.NewTimer(options.StableFor)
				stableTimerCh = stableTimer.C
			}
		} else {
			resetStableTimer()
		}
	}
}

// removedSession describes a session the registry deleted after Wait saw it.
// Deletion only follows the gone transition, so the session is gone.
func removedSession(session registry.Session, at time.Time) registry.Session {
	if session.Presence() == PresenceGone {
		return session
	}
	session.Liveness = registry.Gone{At: at, Reason: "session_removed", Decision: session.Decision()}
	session.PresenceChangedAt = at
	return session
}

func (c *Client) startWatcher(ctx context.Context) (sessionWatcher, error) {
	if c.newWatcher != nil {
		return c.newWatcher(ctx, emptyFilter())
	}
	switch c.mode {
	case ModeRealtimeOnly:
		w, err := newRealtimeWatcher(ctx, c.realtime, emptyFilter())
		if err != nil {
			return nil, publicError(err)
		}
		return w, nil
	case ModeDurableOnly:
		return newDurableWatcher(ctx, c.storePath), nil
	case ModeAuto:
		w, err := newRealtimeWatcher(ctx, c.realtime, emptyFilter())
		if err == nil {
			return w, nil
		}
		if !IsUnavailable(err) && !broker.IsUnavailable(err) {
			return nil, publicError(err)
		}
		return newDurableWatcher(ctx, c.storePath), nil
	default:
		return nil, ErrInvalidMode
	}
}

func evaluateCondition(session registry.Session, options WaitOptions) (bool, error) {
	if options.Presence == PresenceGone {
		return session.Presence() == PresenceGone, nil
	}

	if session.Presence() == PresenceGone {
		return false, ErrSessionDisappeared
	}

	if session.Presence() == PresenceUnknown {
		if options.Presence == PresenceUnknown {
			return true, nil
		}
		return false, ErrUnknownState
	}

	if options.Presence != "" && options.Presence != PresenceLive {
		return false, nil
	}

	if options.Activity != "" {
		if session.Activity() == nil {
			return false, nil
		}
		return *session.Activity() == options.Activity, nil
	}

	return true, nil
}

func newRealtimeWatcher(ctx context.Context, brokerClient *broker.Client, filter registry.Filter) (*brokerSessionWatcher, error) {
	sub, err := brokerClient.Subscribe(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("subscribing to broker: %w", err)
	}

	watcherCtx, cancel := context.WithCancel(ctx)
	w := &brokerSessionWatcher{
		subscription: sub,
		cancel:       cancel,
		events:       make(chan watchEvent, defaultWatcherBufferSize),
		done:         make(chan struct{}),
	}

	go w.run(watcherCtx)
	return w, nil
}

func (w *brokerSessionWatcher) Events() <-chan watchEvent {
	return w.events
}

func (w *brokerSessionWatcher) Close() {
	w.cancel()
	w.subscription.Close()
	<-w.done
}

func (w *brokerSessionWatcher) run(ctx context.Context) {
	defer close(w.done)
	defer close(w.events)

	snapshots := w.subscription.Snapshots
	errorsChan := w.subscription.Errors

	for snapshots != nil || errorsChan != nil {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-snapshots:
			if !ok {
				snapshots = nil
				continue
			}
			if !w.forwardEvent(ctx, watchEvent{sessions: snap.Sessions, err: nil}) {
				return
			}
		case err, ok := <-errorsChan:
			if !ok {
				errorsChan = nil
				continue
			}
			if err != nil && !w.forwardEvent(ctx, watchEvent{sessions: nil, err: err}) {
				return
			}
		}
	}
}

func (w *brokerSessionWatcher) forwardEvent(ctx context.Context, ev watchEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case w.events <- ev:
		return true
	}
}

func newDurableWatcher(ctx context.Context, storePath string) *durableSessionWatcher {
	watchCtx, cancel := context.WithCancel(ctx)
	w := &durableSessionWatcher{
		cancel: cancel,
		events: make(chan watchEvent, defaultWatcherBufferSize),
		done:   make(chan struct{}),
	}

	go w.run(watchCtx, storePath)
	return w
}

func (w *durableSessionWatcher) Events() <-chan watchEvent {
	return w.events
}

func (w *durableSessionWatcher) Close() {
	w.cancel()
	<-w.done
}

func (w *durableSessionWatcher) run(ctx context.Context, storePath string) {
	defer close(w.done)
	defer close(w.events)

	store := registry.NewJournal(storePath, catalog.Rules{})
	err := store.Watch(ctx, registry.WatchOptions{
		Filter:            emptyFilter(),
		Debounce:          defaultFileWatchDebounce,
		ReconcileInterval: 0,
	}, func(res registry.WatchResult) error {
		var ev watchEvent
		if res.Err != nil {
			ev = watchEvent{sessions: nil, err: res.Err}
		} else {
			ev = watchEvent{sessions: res.Sessions, err: nil}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case w.events <- ev:
			return nil
		}
	})
	if err != nil && ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case w.events <- watchEvent{sessions: nil, err: err}:
		}
	}
}

func waitError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrWaitTimeout, err)
	}
	return err
}

func sameWaitProcess(left, right *registry.ProcessIdentity) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.PID == right.PID && left.StartIdentity == right.StartIdentity
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}
