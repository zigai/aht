package core

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrIdentity     = errors.New("session identity")
	ErrTime         = errors.New("session timestamps")
	ErrLiveness     = errors.New("session liveness")
	ErrNativeEnd    = errors.New("native end is terminal")
	ErrVerification = errors.New("presence verification")
	errGoneActivity = errors.New("gone session has no activity")
)

type Session struct {
	ID          string
	Harness     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	HasLiveness bool
	Presence    string
	Activity    string
	ValidState  bool
	NativeEnded bool
}

type Coverage struct {
	mu   sync.Mutex
	hits map[string]struct{}
}

//nolint:cyclop // Every independent invariant contributes a separate error.
func Sessions(sessions []Session) error {
	var violations []error
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		id := session.ID
		// Mirrors the identity and time requirements of validateSnapshot.
		if id == "" || session.Harness == "" {
			violations = append(violations, fmt.Errorf("%w: session %q has empty id or harness", ErrIdentity, id))
		}
		if _, exists := seen[id]; exists {
			violations = append(violations, fmt.Errorf("%w: session %q is duplicated", ErrIdentity, id))
		}
		seen[id] = struct{}{}
		if session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() || session.UpdatedAt.Before(session.CreatedAt) {
			violations = append(violations, fmt.Errorf("%w: session %q has invalid creation/update times", ErrTime, id))
		}
		if !session.HasLiveness || !session.ValidState {
			violations = append(violations, fmt.Errorf("%w: session %q has invalid liveness", ErrLiveness, id))
		}
		// Gone.Activity() is nil, and the session codec rejects a gone activity value.
		if session.Presence == "gone" && session.Activity != "" {
			violations = append(violations, fmt.Errorf("%w: session %q has activity %s", errGoneActivity, id, session.Activity))
		}
		// lifecycleMachine.end clears activity and retains a gone tombstone.
		if session.NativeEnded && (session.Presence != "gone" || session.Activity != "") {
			violations = append(violations, fmt.Errorf("%w: session %q is %s with activity %s", ErrNativeEnd, id, session.Presence, session.Activity))
		}
	}
	return errors.Join(violations...)
}

func NewCoverage() *Coverage { return &Coverage{mu: sync.Mutex{}, hits: make(map[string]struct{})} }

func Verification(before, after string, processComplete bool) error {
	if (before == "gone" || processComplete) && after != before {
		return fmt.Errorf("%w: changes protected presence from %s to %s", ErrVerification, before, after)
	}
	if before != "live" && after == "live" {
		return fmt.Errorf("%w: establishes live presence without a process sighting", ErrVerification)
	}
	return nil
}

func (c *Coverage) Hit(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits[name] = struct{}{}
}

func (c *Coverage) Missing(required ...string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var missing []string
	for _, name := range required {
		if _, ok := c.hits[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}
