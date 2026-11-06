package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/broker"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultWatcherBufferSize = 16
	defaultFileWatchDebounce = 10 * time.Millisecond
)

const (
	waitStepEvent waitStep = iota
	waitStepStable
	waitStepTimer
	waitStepCanceled
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

	// ErrInvalidDuration means a duration was negative or stable-for reached timeout.
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

type waitStep int

type waitRun struct {
	client        *Client
	opts          WaitOptions
	stream        sessionWatcher
	durable       bool
	stableTimer   *time.Timer
	stableTimerCh <-chan time.Time
	stableReady   bool
	candidate     *registry.Session
	lastSeen      *registry.Session
	seenInitial   bool
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

	if o.Timeout > 0 && o.StableFor >= o.Timeout {
		return fmt.Errorf("%w: stable-for duration must be less than timeout", ErrInvalidDuration)
	}

	return nil
}

func (o WaitOptions) validateConditions() error {
	if o.Activity == "" && o.Presence == "" {
		return ErrConditionRequired
	}

	if o.Presence != "" && !o.Presence.Valid() {
		return fmt.Errorf("%w: %q", registry.ErrUnknownPresence, o.Presence)
	}

	if o.Activity != "" && !o.Activity.Valid() {
		return fmt.Errorf("%w: %q", registry.ErrUnknownActivity, o.Activity)
	}

	if o.Presence == PresenceGone && o.Activity != "" {
		return fmt.Errorf(
			"%w: presence %q cannot be combined with activity %q",
			ErrContradictoryCondition,
			o.Presence,
			o.Activity,
		)
	}

	if o.Presence == PresenceUnknown && o.Activity != "" {
		return fmt.Errorf(
			"%w: presence %q cannot be combined with activity %q",
			ErrContradictoryCondition,
			o.Presence,
			o.Activity,
		)
	}

	return nil
}

func emptyFilter() registry.Filter {
	return registry.Filter{}
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
func (c *Client) Wait(ctx context.Context, opts WaitOptions) (WaitResult, error) {
	if c.configErr != nil {
		return WaitResult{}, c.configErr
	}

	if err := opts.Validate(); err != nil {
		return WaitResult{}, err
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	stream, err := c.startWatcher(ctx)
	if err != nil {
		return WaitResult{}, waitError(err)
	}

	_, durable := stream.(*durableSessionWatcher)
	run := &waitRun{
		client: c, opts: opts, stream: stream, durable: durable,
		stableTimer: nil, stableTimerCh: nil, stableReady: false,
		candidate: nil, lastSeen: nil, seenInitial: false,
	}

	defer run.close()

	return run.loop(ctx)
}

func (r *waitRun) loop(ctx context.Context) (WaitResult, error) {
	for {
		ev, ok, step := r.nextEvent(ctx)
		switch step {
		case waitStepCanceled:
			return WaitResult{}, waitError(ctx.Err())
		case waitStepStable:
			return WaitResult{Session: *r.candidate, Initial: false}, nil
		case waitStepTimer:
			continue
		case waitStepEvent:
		}

		if !ok || ev.err != nil {
			if err := r.replaceFailedStream(ctx, ok, ev.err); err != nil {
				return WaitResult{}, err
			}

			continue
		}

		result, done, err := r.observe(ev.sessions)
		if err != nil {
			return WaitResult{}, err
		}

		if done {
			return result, nil
		}
	}
}

func (r *waitRun) nextEvent(ctx context.Context) (watchEvent, bool, waitStep) {
	if ctx.Err() != nil {
		return watchEvent{}, false, waitStepCanceled
	}

	// Consume already queued observations before accepting an elapsed stability
	// timer. Otherwise select could return a stale match while a change waits.
	select {
	case ev, ok := <-r.stream.Events():
		return ev, ok, waitStepEvent
	default:
	}

	if r.stableReady && r.candidate != nil {
		return watchEvent{}, false, waitStepStable
	}

	select {
	case <-ctx.Done():
		return watchEvent{}, false, waitStepCanceled
	case <-r.stableTimerCh:
		r.stableReady = true
		r.stableTimerCh = nil

		return watchEvent{}, false, waitStepTimer
	case ev, ok := <-r.stream.Events():
		return ev, ok, waitStepEvent
	}
}

func (r *waitRun) replaceFailedStream(ctx context.Context, ok bool, err error) error {
	r.resetStable()

	if !r.fallsBackToDurable(ok, err) {
		if !ok {
			return ErrStreamClosed
		}

		return waitError(publicError(err))
	}

	r.stream.Close()
	r.stream = startDurableWatcher(ctx, r.client.storePath)
	r.durable = true

	return nil
}

func (r *waitRun) fallsBackToDurable(ok bool, err error) bool {
	if r.client.mode != ModeAuto || r.durable {
		return false
	}

	return !ok || IsUnavailable(err) || broker.IsUnavailable(err) || StreamClosed(err)
}

func (r *waitRun) observe(sessions []registry.Session) (WaitResult, bool, error) {
	isInitial := !r.seenInitial
	r.seenInitial = true

	current := r.currentSession(sessions)
	if current == nil {
		r.resetStable()
		return WaitResult{}, false, registry.ErrSessionNotFound
	}

	r.lastSeen = current

	matched, outcomeErr := evaluateCondition(*current, r.opts)
	if outcomeErr != nil {
		r.resetStable()
		return WaitResult{}, false, outcomeErr
	}

	if !matched {
		r.resetStable()
		return WaitResult{}, false, nil
	}

	if r.opts.StableFor == 0 {
		return WaitResult{Session: *current, Initial: isInitial}, true, nil
	}

	r.holdCandidate(current)

	return WaitResult{}, false, nil
}

func (r *waitRun) currentSession(sessions []registry.Session) *registry.Session {
	for i := range sessions {
		if sessions[i].ID == r.opts.ID {
			return &sessions[i]
		}
	}

	if r.lastSeen == nil {
		return nil
	}

	removed := removedSession(*r.lastSeen, time.Now().UTC())

	return &removed
}

func (r *waitRun) holdCandidate(current *registry.Session) {
	if r.candidate != nil && !sameWaitProcess(r.candidate.Process, current.Process) {
		r.resetStable()
	}

	r.candidate = current

	if r.stableTimer == nil {
		r.stableTimer = time.NewTimer(r.opts.StableFor)
		r.stableTimerCh = r.stableTimer.C
	}
}

func (r *waitRun) resetStable() {
	r.stableReady = false

	stopTimer(r.stableTimer)
	r.stableTimer = nil
	r.stableTimerCh = nil
	r.candidate = nil
}

func (r *waitRun) close() {
	stopTimer(r.stableTimer)

	if r.stream != nil {
		r.stream.Close()
	}
}

// removedSession describes a session the registry deleted after Wait saw it.
// Deletion only follows the gone transition, so the session is gone.
func removedSession(session registry.Session, at time.Time) registry.Session {
	if session.Presence() == PresenceGone {
		return session
	}

	session.Liveness = registry.Gone{
		At:       at,
		Reason:   "session_removed",
		Decision: session.Decision(),
	}
	session.PresenceChangedAt = at

	return session
}

func (c *Client) startWatcher(ctx context.Context) (sessionWatcher, error) {
	if c.newWatcher != nil {
		return c.newWatcher(ctx, emptyFilter())
	}

	switch c.mode {
	case ModeRealtimeOnly:
		w, err := openBrokerWatcher(ctx, c.realtime, emptyFilter())
		if err != nil {
			return nil, publicError(err)
		}

		return w, nil
	case ModeDurableOnly:
		return startDurableWatcher(ctx, c.storePath), nil
	case ModeAuto:
		w, err := openBrokerWatcher(ctx, c.realtime, emptyFilter())
		if err == nil {
			return w, nil
		}

		if !IsUnavailable(err) && !broker.IsUnavailable(err) && !StreamClosed(err) {
			return nil, publicError(err)
		}

		return startDurableWatcher(ctx, c.storePath), nil
	default:
		return nil, ErrInvalidMode
	}
}

func evaluateCondition(session registry.Session, opts WaitOptions) (bool, error) {
	if opts.Presence == PresenceGone {
		return session.Presence() == PresenceGone, nil
	}

	if session.Presence() == PresenceGone {
		return false, ErrSessionDisappeared
	}

	if session.Presence() == PresenceUnknown {
		if opts.Presence == PresenceUnknown {
			return true, nil
		}

		return false, ErrUnknownState
	}

	if opts.Presence != "" && opts.Presence != PresenceLive {
		return false, nil
	}

	if opts.Activity != "" {
		if session.Activity() == nil {
			return false, nil
		}

		return *session.Activity() == opts.Activity, nil
	}

	return true, nil
}

func openBrokerWatcher(
	ctx context.Context,
	brokerClient *broker.Client,
	filter registry.Filter,
) (*brokerSessionWatcher, error) {
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
	errCh := w.subscription.Errors

	for snapshots != nil || errCh != nil {
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
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
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

func startDurableWatcher(ctx context.Context, storePath string) *durableSessionWatcher {
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

func sameWaitProcess(left *registry.ProcessIdentity, right *registry.ProcessIdentity) bool {
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
