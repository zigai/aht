package registry

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"slices" // SessionCWD returns the best available working directory for session.
	"strconv"
	"strings"
	"time"
)

func SessionCWD(session Session) string {
	if session.CWD != "" {
		return session.CWD
	}

	if session.Process != nil && session.Process.CWD != "" {
		return session.Process.CWD
	}

	return session.Location.PaneCurrentPath
}

// CanonicalPath returns an absolute, symlink-resolved, cleaned path for p.
func CanonicalPath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}

	cleaned := filepath.Clean(p)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}

	return canonicalizeExistingOrParent(cleaned)
}

// PathsEqual reports whether p1 and p2 resolve to the same canonical path.
func PathsEqual(p1 string, p2 string) bool {
	if p1 == "" || p2 == "" {
		return false
	}

	if p1 == p2 {
		return true
	}

	return CanonicalPath(p1) == CanonicalPath(p2)
}

// PathWithinOrEqual reports whether target is equal to base or located within base.
func PathWithinOrEqual(target string, base string) bool {
	targetCanon := CanonicalPath(target)

	baseCanon := CanonicalPath(base)
	if targetCanon == "" || baseCanon == "" {
		return false
	}

	if targetCanon == baseCanon {
		return true
	}

	rel, err := filepath.Rel(baseCanon, targetCanon)
	if err != nil {
		return false
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// MatchesProject reports whether session matches the specified project path.
func MatchesProject(session Session, project string, subtree bool) bool {
	if project == "" {
		return true
	}

	sessionProj := session.ProjectRoot
	if sessionProj == "" {
		sessionProj = SessionCWD(session)
	}

	if subtree {
		return PathWithinOrEqual(session.ProjectRoot, project) ||
			PathWithinOrEqual(SessionCWD(session), project)
	}

	return PathsEqual(sessionProj, project)
}

// MatchesServer reports whether session matches server on either Location or Tmux context.
func MatchesServer(session Session, server string) bool {
	if session.Location.ServerID == server {
		return true
	}

	return PathsEqual(session.Location.ServerID, server)
}

// NormalizePaths resolves project and CWD selectors before they cross a process boundary.
func (filter Filter) NormalizePaths() Filter {
	filter.Project = CanonicalPath(filter.Project)
	filter.CWD = CanonicalPath(filter.CWD)

	return filter
}

// FilterSessions returns sessions matching filter in deterministic sort order.
func FilterSessions(sessions []Session, filter Filter) []Session {
	filtered := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if !matchesFilterState(session, filter) || !matchesFilterScope(session, filter) {
			continue
		}

		filtered = append(filtered, session)
	}

	sortSessions(filtered)

	return filtered
}

func sessionIDForObservation(observation Observation) string {
	parts := []string{string(observation.Harness)}
	switch {
	case observation.Subject.SessionID != "":
		parts = append(parts, "id", observation.Subject.SessionID)
	case observation.Subject.SessionPath != "":
		parts = append(parts, "path", filepath.Clean(observation.Subject.SessionPath))
	case observation.ProcessIdentity() != nil && observation.ProcessIdentity().Complete():
		parts = append(
			parts,
			"process",
			strconv.Itoa(observation.ProcessIdentity().PID),
			observation.ProcessIdentity().StartIdentity,
		)
	default:
		var event string
		if report := observation.Report(); report != nil {
			event = report.Event
		}

		parts = append(parts, "event", event)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return string(observation.Harness) + "-" + hex.EncodeToString(sum[:8])
}

func matchesFilterState(session Session, filter Filter) bool {
	if filter.Harness != "" && session.Harness != filter.Harness {
		return false
	}

	if filter.Presence != "" && session.Presence() != filter.Presence {
		return false
	}

	if filter.Activity != "" &&
		(session.Activity() == nil || *session.Activity() != filter.Activity) {
		return false
	}

	return true
}

func matchesFilterScope(session Session, filter Filter) bool {
	if filter.MultiplexerSession != "" &&
		session.Location.SessionName != filter.MultiplexerSession &&
		session.Location.SessionID != filter.MultiplexerSession {
		return false
	}

	if filter.CWD != "" && !PathsEqual(SessionCWD(session), filter.CWD) {
		return false
	}

	if filter.Project != "" && !MatchesProject(session, filter.Project, filter.ProjectSubtree) {
		return false
	}

	if !matchesFilterLocation(session, filter) {
		return false
	}

	return true
}

func matchesFilterLocation(session Session, filter Filter) bool {
	if filter.MultiplexerKind != "" && session.Location.Kind != filter.MultiplexerKind {
		return false
	}

	if filter.MultiplexerServer != "" && !MatchesServer(session, filter.MultiplexerServer) {
		return false
	}

	if filter.MultiplexerPane != "" &&
		session.Location.PaneID != filter.MultiplexerPane {
		return false
	}

	return true
}

func sortSessions(sessions []Session) {
	slices.SortFunc(sessions, func(left, right Session) int {
		return cmp.Or(
			cmp.Compare(left.Location.Kind, right.Location.Kind),
			cmp.Compare(left.Location.SessionName, right.Location.SessionName),
			compareNumericStrings(
				cmp.Or(left.Location.WindowIndex, left.Location.TabIndex),
				cmp.Or(right.Location.WindowIndex, right.Location.TabIndex),
			),
			compareNumericStrings(left.Location.PaneIndex, right.Location.PaneIndex),
			cmp.Compare(left.Harness, right.Harness),
			cmp.Compare(left.ID, right.ID),
			left.UpdatedAt.Compare(right.UpdatedAt),
		)
	})
}

func compareNumericStrings(left string, right string) int {
	leftNumber, leftErr := strconv.Atoi(left)

	rightNumber, rightErr := strconv.Atoi(right)
	if leftErr == nil && rightErr == nil {
		return cmp.Compare(leftNumber, rightNumber)
	}

	return strings.Compare(left, right)
}

func maxTime(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}

	return latest
}

func canonicalizeExistingOrParent(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}

	parent := filepath.Dir(path)
	if parent == path || parent == "." || parent == "/" || parent == string(filepath.Separator) {
		return path
	}

	resolvedParent := canonicalizeExistingOrParent(parent)

	return filepath.Join(resolvedParent, filepath.Base(path))
}
