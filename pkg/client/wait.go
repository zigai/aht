package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
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

	// ErrSessionRequired means no session reference was provided.
	ErrSessionRequired = errors.New("session reference is required")
)

// WaitOptions configures the session condition, timeout, and stability duration to wait for.
type WaitOptions struct {
	// Reference selects the session like [Selector.Reference]: a registry ID,
	// registry ID prefix, native session ID, or session path. It is resolved
	// against the first snapshot.
	Reference string

	// Activity optionally specifies the target activity state.
	Activity registry.Activity

	// Presence optionally specifies the target presence state.
	Presence registry.Presence

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
	Session registry.Session `json:"session"`

	// Initial reports whether the condition was already satisfied by the initial snapshot.
	Initial bool `json:"initial"`
}

type waitStep int

type waitRun struct {
	opts          WaitOptions
	stream        *sessionStream
	id            string
	stableTimer   *time.Timer
	stableTimerCh <-chan time.Time
	stableReady   bool
	candidate     *registry.Session
	lastSeen      *registry.Session
	seenInitial   bool
}

// Validate checks that WaitOptions has a session reference, valid durations,
// and at least one non-contradictory condition.
func (o WaitOptions) Validate() error {
	if strings.TrimSpace(o.Reference) == "" {
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

	if (o.Presence == registry.PresenceGone || o.Presence == registry.PresenceUnknown) &&
		o.Activity != "" {
		return fmt.Errorf(
			"%w: presence %q cannot be combined with activity %q",
			ErrContradictoryCondition,
			o.Presence,
			o.Activity,
		)
	}

	return nil
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
// A reference that matches no session in the first snapshot returns
// registry.ErrSessionNotFound.
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

	stream, err := c.openSessionStream(ctx, registry.Filter{})
	if err != nil {
		return WaitResult{}, waitError(err)
	}

	run := &waitRun{
		opts: opts, stream: stream, id: "",
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
			r.resetStable()

			if err := r.stream.recover(ctx, ok, ev.err); err != nil {
				return WaitResult{}, waitError(err)
			}

			continue
		}

		result, done, err := r.observe(ev.snapshot.Sessions)
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
	case ev, ok := <-r.stream.events():
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
	case ev, ok := <-r.stream.events():
		return ev, ok, waitStepEvent
	}
}

func (r *waitRun) observe(sessions []registry.Session) (WaitResult, bool, error) {
	isInitial := !r.seenInitial
	r.seenInitial = true

	current, err := r.currentSession(sessions)
	if err != nil {
		r.resetStable()
		return WaitResult{}, false, err
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

func (r *waitRun) currentSession(sessions []registry.Session) (*registry.Session, error) {
	if r.id == "" {
		session, err := ResolveSessions(sessions, Selector{Reference: r.opts.Reference})
		if err != nil {
			return nil, err
		}

		r.id = session.ID
	}

	for i := range sessions {
		if sessions[i].ID == r.id {
			return &sessions[i], nil
		}
	}

	removed := removedSession(*r.lastSeen, time.Now().UTC())

	return &removed, nil
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

	if r.stableTimer != nil {
		r.stableTimer.Stop()
	}

	r.stableTimer = nil
	r.stableTimerCh = nil
	r.candidate = nil
}

func (r *waitRun) close() {
	if r.stableTimer != nil {
		r.stableTimer.Stop()
	}

	r.stream.close()
}

// removedSession describes a session the registry deleted after Wait saw it.
// Deletion only follows the gone transition, so the session is gone.
func removedSession(session registry.Session, at time.Time) registry.Session {
	if session.Presence() == registry.PresenceGone {
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

func evaluateCondition(session registry.Session, opts WaitOptions) (bool, error) {
	if opts.Presence == registry.PresenceGone {
		return session.Presence() == registry.PresenceGone, nil
	}

	if session.Presence() == registry.PresenceGone {
		return false, ErrSessionDisappeared
	}

	if session.Presence() == registry.PresenceUnknown {
		if opts.Presence == registry.PresenceUnknown {
			return true, nil
		}

		return false, ErrUnknownState
	}

	if opts.Presence != "" && opts.Presence != registry.PresenceLive {
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
