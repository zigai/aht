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

	// ErrSessionNotFound is returned when no session matches the selector.
	// It aliases [registry.ErrSessionNotFound] to preserve error classification.
	ErrSessionNotFound = registry.ErrSessionNotFound

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
type Selector struct {
	// ID matches an exact canonical registry ID.
	ID string

	// Reference matches a session using reference precedence:
	// exact registry ID, followed by registry ID prefix, native session ID,
	// or session path.
	Reference string

	// Harness filters matches by agent harness.
	Harness Harness

	// MultiplexerKind filters matches by multiplexer type (e.g. tmux, zellij, herdr).
	MultiplexerKind MultiplexerKind

	// MultiplexerServer filters matches by multiplexer server or socket identifier.
	MultiplexerServer string

	// MultiplexerPane filters matches by multiplexer pane identifier.
	MultiplexerPane string

	// Project matches sessions whose project root matches the given path.
	Project string

	// ProjectSubtree causes Project to match if a session's project root or CWD
	// is located within or equal to the specified project path.
	ProjectSubtree bool

	// CWD matches sessions whose current working directory matches the given path.
	CWD string
}

// SessionLister captures the session query capability required to resolve selectors.
type SessionLister interface {
	List(ctx context.Context, filter registry.Filter) ([]registry.Session, error)
}

func (e *AmbiguousSessionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if len(e.Matches) > 0 {
		return fmt.Sprintf("%v: %d sessions match (%s)", ErrAmbiguousSession, len(e.Matches), strings.Join(e.Matches, ", "))
	}
	return ErrAmbiguousSession.Error()
}

// Unwrap returns ErrAmbiguousSession to preserve error classification.
func (e *AmbiguousSessionError) Unwrap() error {
	return ErrAmbiguousSession
}

// Filter returns a [registry.Filter] matching the non-reference criteria of the selector.
func (s Selector) Filter() registry.Filter {
	return registry.Filter{
		Harness:            s.Harness,
		Presence:           "",
		Activity:           "",
		MultiplexerSession: "",
		Project:            s.Project,
		ProjectSubtree:     s.ProjectSubtree,
		CWD:                s.CWD,
		MultiplexerKind:    s.MultiplexerKind,
		MultiplexerServer:  s.MultiplexerServer,
		MultiplexerPane:    s.MultiplexerPane,
	}
}

// Resolve finds a single session matching selector using the client's configured store.
func (c *Client) Resolve(ctx context.Context, selector Selector) (registry.Session, error) {
	if c.configErr != nil {
		return registry.Session{}, c.configErr
	}
	return Resolve(ctx, c, selector)
}

// Resolve finds a single session matching selector using the provided session lister.
func Resolve(ctx context.Context, lister SessionLister, selector Selector) (registry.Session, error) {
	if lister == nil {
		return registry.Session{}, errSessionListerRequired
	}
	sessions, err := lister.List(ctx, selector.Filter())
	if err != nil {
		return registry.Session{}, fmt.Errorf("list sessions: %w", err)
	}
	return ResolveSessions(sessions, selector)
}

// ResolveSessions resolves a single session from a slice of candidate sessions
// according to the selector's qualifier and reference precedence rules.
func ResolveSessions(sessions []registry.Session, selector Selector) (registry.Session, error) {
	qualified := filterByQualifiers(sessions, selector)
	if selector.Reference == "" {
		return resolveWithoutReference(qualified, selector)
	}
	return resolveWithReference(qualified, selector)
}

func filterByQualifiers(sessions []registry.Session, selector Selector) []registry.Session {
	qualified := make([]registry.Session, 0, len(sessions))
	for _, s := range sessions {
		if matchesSessionQualifiers(s, selector) {
			qualified = append(qualified, s)
		}
	}
	return qualified
}

func matchesSessionQualifiers(s registry.Session, selector Selector) bool {
	return matchesIDAndHarness(s, selector) &&
		matchesLocation(s, selector) &&
		matchesPaths(s, selector)
}

func matchesIDAndHarness(s registry.Session, selector Selector) bool {
	if selector.ID != "" && s.ID != selector.ID {
		return false
	}
	if selector.Harness != "" && s.Harness != selector.Harness {
		return false
	}
	return true
}

func matchesLocation(s registry.Session, selector Selector) bool {
	location := s.Location
	if location.Empty() {
		location = s.Location
	}
	if selector.MultiplexerKind != "" && location.Kind != selector.MultiplexerKind {
		return false
	}
	if selector.MultiplexerServer != "" && !registry.MatchesServer(s, selector.MultiplexerServer) {
		return false
	}
	if selector.MultiplexerPane != "" &&
		s.Location.PaneID != selector.MultiplexerPane {
		return false
	}
	return true
}

func matchesPaths(s registry.Session, selector Selector) bool {
	if selector.Project != "" && !registry.MatchesProject(s, selector.Project, selector.ProjectSubtree) {
		return false
	}
	if selector.CWD != "" && !registry.PathsEqual(registry.SessionCWD(s), selector.CWD) {
		return false
	}
	return true
}

func resolveWithoutReference(qualified []registry.Session, selector Selector) (registry.Session, error) {
	switch len(qualified) {
	case 0:
		return registry.Session{}, ErrSessionNotFound
	case 1:
		return qualified[0], nil
	default:
		ids := sessionIDs(qualified)
		return registry.Session{}, &AmbiguousSessionError{
			Selector: selector,
			Matches:  ids,
			Message:  fmt.Sprintf("%v: selector matched %d sessions (%s)", ErrAmbiguousSession, len(qualified), strings.Join(ids, ", ")),
		}
	}
}

func resolveWithReference(qualified []registry.Session, selector Selector) (registry.Session, error) {
	// Exact registry ID match always wins first.
	for _, s := range qualified {
		if s.ID == selector.Reference {
			return s, nil
		}
	}

	// Otherwise, match by prefix, native session ID, or session path.
	matches := make([]registry.Session, 0, 1)
	for _, s := range qualified {
		if matchesReferenceCandidate(s, selector.Reference) {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return registry.Session{}, ErrSessionNotFound
	case 1:
		return matches[0], nil
	default:
		ids := sessionIDs(matches)
		return registry.Session{}, &AmbiguousSessionError{
			Selector: selector,
			Matches:  ids,
			Message:  fmt.Sprintf("%v: %q matches %d sessions (%s)", ErrAmbiguousSession, selector.Reference, len(matches), strings.Join(ids, ", ")),
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
	if s.SessionPath != "" && (s.SessionPath == reference || registry.PathsEqual(s.SessionPath, reference)) {
		return true
	}
	return false
}

func sessionIDs(sessions []registry.Session) []string {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	return ids
}
