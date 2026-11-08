package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	// ErrAmbiguousSession indicates that a selector or reference matched multiple sessions.
	ErrAmbiguousSession = errors.New("ambiguous session reference")

	// ErrNoCurrentSession indicates that no agent session was found for the current context.
	ErrNoCurrentSession = errors.New("no current agent session found")

	errSessionListerRequired = errors.New("session lister is required")
)

// AmbiguousSessionError provides detailed information about ambiguous session matches.
type AmbiguousSessionError struct {
	Selector Selector
	Matches  []string
	Message  string
}

// Selector specifies criteria for selecting and resolving a single session.
//
//exhaustruct:optional
type Selector struct {
	// ID matches an exact canonical registry ID.
	ID string

	// Reference matches a session using reference precedence:
	// exact registry ID, followed by registry ID prefix, native session ID,
	// or session path.
	Reference string

	// Filter narrows the candidates before ID and Reference are applied.
	Filter registry.Filter
}

// SessionLister captures the session query capability required to resolve
// selectors. List must return only sessions matching filter.
type SessionLister interface {
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
}

func (e *AmbiguousSessionError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	if len(e.Matches) > 0 {
		return fmt.Sprintf(
			"%v: %d sessions match (%s)",
			ErrAmbiguousSession,
			len(e.Matches),
			strings.Join(e.Matches, ", "),
		)
	}

	return ErrAmbiguousSession.Error()
}

// Unwrap returns ErrAmbiguousSession to preserve error classification.
func (e *AmbiguousSessionError) Unwrap() error {
	return ErrAmbiguousSession
}

// Resolve finds a single session matching selector using the client's configured store.
func (c *Client) Resolve(ctx context.Context, selector Selector) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}

	return Resolve(ctx, c, selector)
}

// Resolve finds a single session matching selector using the provided session lister.
func Resolve(
	ctx context.Context,
	lister SessionLister,
	selector Selector,
) (registry.Session, error) {
	if lister == nil {
		return registry.Session{}, errSessionListerRequired
	}

	sessions, err := lister.List(ctx, selector.Filter)
	if err != nil {
		return registry.Session{}, fmt.Errorf("list sessions: %w", err)
	}

	return resolveFiltered(sessions, selector)
}

// ResolveSessions resolves a single session from a slice of candidate sessions
// according to the selector's filter and reference precedence rules.
func ResolveSessions(sessions []registry.Session, selector Selector) (registry.Session, error) {
	return resolveFiltered(registry.FilterSessions(sessions, selector.Filter), selector)
}

func resolveFiltered(sessions []registry.Session, selector Selector) (registry.Session, error) {
	qualified := sessions
	if selector.ID != "" {
		qualified = make([]registry.Session, 0, 1)

		for _, s := range sessions {
			if s.ID == selector.ID {
				qualified = append(qualified, s)
			}
		}
	}

	if selector.Reference == "" {
		return resolveWithoutReference(qualified, selector)
	}

	return resolveWithReference(qualified, selector)
}

func resolveWithoutReference(
	qualified []registry.Session,
	selector Selector,
) (registry.Session, error) {
	switch len(qualified) {
	case 0:
		return registry.Session{}, registry.ErrSessionNotFound
	case 1:
		return qualified[0], nil
	default:
		ids := sessionIDs(qualified)

		return registry.Session{}, &AmbiguousSessionError{
			Selector: selector,
			Matches:  ids,
			Message: fmt.Sprintf(
				"%v: selector matched %d sessions (%s)",
				ErrAmbiguousSession,
				len(qualified),
				strings.Join(ids, ", "),
			),
		}
	}
}

func resolveWithReference(
	qualified []registry.Session,
	selector Selector,
) (registry.Session, error) {
	for _, s := range qualified {
		if s.ID == selector.Reference {
			return s, nil
		}
	}

	matches := make([]registry.Session, 0, 1)

	for _, s := range qualified {
		if matchesReferenceCandidate(s, selector.Reference) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return registry.Session{}, registry.ErrSessionNotFound
	case 1:
		return matches[0], nil
	default:
		ids := sessionIDs(matches)

		return registry.Session{}, &AmbiguousSessionError{
			Selector: selector,
			Matches:  ids,
			Message: fmt.Sprintf(
				"%v: %q matches %d sessions (%s)",
				ErrAmbiguousSession,
				selector.Reference,
				len(matches),
				strings.Join(ids, ", "),
			),
		}
	}
}

func matchesReferenceCandidate(s registry.Session, reference string) bool {
	if strings.HasPrefix(s.ID, reference) {
		return true
	}

	if s.SessionID != "" && s.SessionID == reference {
		return true
	}

	return s.SessionPath != "" &&
		(s.SessionPath == reference || registry.PathsEqual(s.SessionPath, reference))
}

func sessionIDs(sessions []registry.Session) []string {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}

	return ids
}
