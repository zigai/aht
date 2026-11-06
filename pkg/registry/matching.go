package registry

import (
	"fmt"
	"path/filepath"
	"time"
)

func reconcileResumedProcessSession(
	sessions map[string]Session,
	session *Session,
	observation Observation,
) {
	for id, provisional := range sessions {
		if id == session.ID || provisional.Harness != session.Harness ||
			provisional.IdentityState != IdentityProvisional ||
			provisional.Observations.Native != nil ||
			provisional.Process == nil ||
			!provisional.Process.Equal(*observation.ProcessIdentity()) {
			continue
		}
		// Carry location and process evidence, not the provisional activity or
		// presence: the accepted native event owns the incarnation transition.
		if provisional.Observations.Process != nil {
			session.Observations.Process = provisional.Observations.Process
		}

		if provisional.Observations.Location != nil && observation.Location() == nil {
			session.Observations.Location = provisional.Observations.Location
			session.Location = provisional.Location
		}

		session.Incarnation.EvidenceAt = maxTime(
			session.Incarnation.EvidenceAt,
			provisional.Incarnation.EvidenceAt,
		)

		delete(sessions, id)
	}
}

func (r Reducer) retireConflictingProcessSessions(
	sessions map[string]Session,
	observation Observation,
	at time.Time,
	receivedAt time.Time,
) (Session, error) {
	if observation.ProcessIdentity() == nil || !observation.ProcessIdentity().Complete() {
		return Session{}, nil
	}

	for id, session := range sessions {
		replace, nativeReplacement := r.conflictingSessionReplacement(session, observation)
		if !replace {
			continue
		}

		transition := transitionIncarnation(session, observation, at, true)
		if !transition.accepted {
			if transition.ignored {
				continue
			}

			return session, fmt.Errorf("%w: retired process evidence", errObservationDropped)
		}

		(lifecycleMachine{session: &session}).commit(transition, at)

		if nativeReplacement {
			if gone, ok := session.Liveness.(Gone); ok {
				gone.Reason = sessionReplacedReason
				session.Liveness = gone
			}

			session.setDecision(&ActivityDecision{
				Authority:       "hook",
				Reason:          sessionReplacedReason,
				RuleID:          "",
				ManifestSource:  "",
				ManifestVersion: 0,
				FallbackReason:  "",
				Process:         *observation.ProcessIdentity(),
				ObservedAt:      at,
			})
		}

		session.UpdatedAt = maxTime(session.UpdatedAt, receivedAt)
		sessions[id] = session
	}

	return Session{}, nil
}

func (r Reducer) conflictingSessionReplacement(
	session Session,
	observation Observation,
) (bool, bool) {
	sameProcess := session.Process != nil && session.Process.Equal(*observation.ProcessIdentity())
	if r.nativeSessionReplacement(session, observation, sameProcess) {
		return true, true
	}

	if session.Harness == observation.Harness || session.Presence() != PresenceLive {
		return false, false
	}

	if processHarnessReplacement(observation, sameProcess) {
		return true, false
	}

	if session.Process == nil && observation.Kind() == "placement" &&
		samePane(session, observation.Location()) {
		return true, false
	}

	return false, false
}

func (r Reducer) nativeSessionReplacement(
	session Session,
	observation Observation,
	sameProcess bool,
) bool {
	if !sameProcess ||
		observation.Kind() != "report" ||
		!observationHasIdentity(observation) ||
		session.Harness != observation.Harness ||
		!observationIdentityConflicts(session, observation.Subject) {
		return false
	}

	if observation.Report().Reporter.MultiSession ||
		(session.Observations.Native != nil && session.Observations.Native.Reporter.MultiSession) {
		return false
	}

	return r.rules.Policy(session.Harness).ExclusiveProcess
}

func processHarnessReplacement(observation Observation, sameProcess bool) bool {
	return observation.Kind() == "sighting" &&
		observation.Present() != nil &&
		*observation.Present() &&
		sameProcess
}

func samePane(session Session, observed *Location) bool {
	if observed == nil || observed.PaneID == "" {
		return false
	}

	current := session.Location
	if current.Kind != observed.Kind || current.PaneID != observed.PaneID {
		return false
	}

	if current.ServerID != "" || observed.ServerID != "" {
		return current.ServerID == observed.ServerID
	}

	if current.SessionID != "" || observed.SessionID != "" {
		return current.SessionID == observed.SessionID
	}

	return false
}

func observationHasIdentity(observation Observation) bool {
	return observation.Subject.SessionID != "" || observation.Subject.SessionPath != ""
}

func observationIdentityConflicts(session Session, identity ObservationIdentity) bool {
	if identity.SessionID != "" && session.SessionID != "" {
		return identity.SessionID != session.SessionID
	}

	if identity.SessionPath != "" && session.SessionPath != "" {
		return filepath.Clean(identity.SessionPath) != filepath.Clean(session.SessionPath)
	}

	return false
}

func findAndReconcileMatchingSession(
	sessions map[string]Session,
	observation Observation,
	at time.Time,
) string {
	identityID := findIdentityMatchingSession(sessions, observation)

	processID := findProcessMatchingSession(sessions, observation)
	if preferNativeIdentity(sessions, observation, identityID, processID) {
		return identityID
	}

	if reconcileIdentityProcessSessions(sessions, observation, at, identityID, processID) {
		return processID
	}

	if processID != "" {
		return processID
	}

	if identityID != "" {
		return identityID
	}

	return findListingMatchingSession(sessions, observation)
}

func reconcileIdentityProcessSessions(
	sessions map[string]Session,
	observation Observation,
	at time.Time,
	identityID string, processID string,
) bool {
	if identityID != "" && processID != "" && identityID != processID {
		if prior := sessions[identityID]; prior.Process != nil &&
			!prior.Process.Equal(*observation.ProcessIdentity()) {
			// The prior process terminated and the same session is being resumed
			// or restarted under a new process. Retire the stale incarnation so
			// the registry never retains two live sessions for one session_id.
			(lifecycleMachine{session: &prior}).commit(
				transitionIncarnation(prior, observation, at, true),
				at,
			)
			prior.UpdatedAt = maxTime(prior.UpdatedAt, at)
			sessions[identityID] = prior
		}

		provisional := sessions[identityID]
		if provisional.Process == nil {
			target := mergeProvisionalSession(sessions[processID], provisional)
			sessions[processID] = target
			delete(sessions, identityID)

			return true
		}
	}

	return false
}

func preferNativeIdentity(
	sessions map[string]Session,
	observation Observation,
	identityID string,
	processID string,
) bool {
	return observation.Kind() == "report" && identityID != "" && processID != "" &&
		identityID != processID && sessions[identityID].Process != nil &&
		sessions[processID].IdentityState == IdentityProvisional &&
		sessions[processID].Observations.Native == nil
}

func findMatchingSession(sessions map[string]Session, observation Observation) string {
	if id := findProcessMatchingSession(sessions, observation); id != "" {
		return id
	}

	if id := findIdentityMatchingSession(sessions, observation); id != "" {
		return id
	}

	return findListingMatchingSession(sessions, observation)
}

func findListingMatchingSession(sessions map[string]Session, observation Observation) string {
	listing := observation.Listing()
	if observation.Kind() != "listing" || listing == nil || !listing.Current ||
		listing.ProcessPID <= 0 {
		return ""
	}

	return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
		return session.Process != nil && session.Process.PID == listing.ProcessPID &&
			(listing.ProcessStartIdentity == "" || session.Process.StartIdentity == listing.ProcessStartIdentity) &&
			!observationIdentityConflicts(session, observation.Subject)
	})
}

func findProcessMatchingSession(sessions map[string]Session, observation Observation) string {
	if observation.ProcessIdentity() == nil || !observation.ProcessIdentity().Complete() {
		return ""
	}

	return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
		return session.Process != nil &&
			session.Process.Equal(*observation.ProcessIdentity()) &&
			!observationIdentityConflicts(session, observation.Subject)
	})
}

func findIdentityMatchingSession(sessions map[string]Session, observation Observation) string {
	if observation.Subject.SessionID != "" {
		if id := bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.SessionID == observation.Subject.SessionID
		}); id != "" {
			return id
		}
	}

	if observation.Subject.SessionPath != "" {
		cleanPath := filepath.Clean(observation.Subject.SessionPath)
		if id := bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.SessionPath != "" && filepath.Clean(session.SessionPath) == cleanPath
		}); id != "" {
			return id
		}
	}

	return ""
}

func bestMatchingSessionID(
	sessions map[string]Session,
	harness Harness,
	match func(Session) bool,
) string {
	bestID := ""

	var best Session

	for id, session := range sessions {
		if session.Harness != harness || !match(session) {
			continue
		}

		if bestID == "" || sessionMatchPreferred(session, id, best, bestID) {
			bestID, best = id, session
		}
	}

	return bestID
}

func sessionMatchPreferred(
	candidate Session,
	candidateID string,
	current Session,
	currentID string,
) bool {
	if candidateRank := presenceMatchRank(
		candidate.Presence(),
	); candidateRank != presenceMatchRank(
		current.Presence(),
	) {
		return candidateRank > presenceMatchRank(current.Presence())
	}

	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}

	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}

	return candidateID < currentID
}

func presenceMatchRank(presence Presence) int {
	const (
		goneMatchRank = iota
		unknownMatchRank
		liveMatchRank
	)

	switch presence {
	case PresenceLive:
		return liveMatchRank
	case PresenceUnknown:
		return unknownMatchRank
	case PresenceGone:
		return goneMatchRank
	default:
		return -1
	}
}

func mergeProvisionalSession(target Session, provisional Session) Session {
	target = mergeProvisionalMetadata(target, provisional)

	if target.Process == nil || !target.Process.Complete() {
		target.Observations = mergeObservations(target.Observations, provisional.Observations)
	} else {
		// Identity-only evidence cannot describe the process incarnation.
		provisional.Observations.Native = nil
		provisional.Observations.Process = nil
		provisional.Observations.Screen = nil
		target.Observations = mergeObservations(target.Observations, provisional.Observations)
	}

	if target.CreatedAt.IsZero() ||
		(!provisional.CreatedAt.IsZero() && provisional.CreatedAt.Before(target.CreatedAt)) {
		target.CreatedAt = provisional.CreatedAt
	}

	target.UpdatedAt = maxTime(target.UpdatedAt, provisional.UpdatedAt)
	if target.SessionID != "" || target.SessionPath != "" {
		target.IdentityState = IdentityIdentified
	}

	return target
}

func mergeProvisionalMetadata(target Session, provisional Session) Session {
	if target.SessionID == "" {
		target.SessionID = provisional.SessionID
	}

	if target.SessionPath == "" {
		target.SessionPath = provisional.SessionPath
	}

	if len(target.ResumeCommand) == 0 {
		target.ResumeCommand = append([]string(nil), provisional.ResumeCommand...)
	}

	if target.CWD == "" {
		target.CWD = provisional.CWD
	}

	if target.ProjectRoot == "" {
		target.ProjectRoot = provisional.ProjectRoot
	}

	if target.Location.Empty() {
		target.Location = provisional.Location
	}

	return target
}

func mergeObservations(target Observations, provisional Observations) Observations {
	if target.Native == nil ||
		(provisional.Native != nil && provisional.Native.ObservedAt.After(target.Native.ObservedAt)) {
		target.Native = provisional.Native
	}

	if target.Process == nil ||
		(provisional.Process != nil && provisional.Process.ObservedAt.After(target.Process.ObservedAt)) {
		target.Process = provisional.Process
	}

	if target.Catalog == nil ||
		(provisional.Catalog != nil && provisional.Catalog.ObservedAt.After(target.Catalog.ObservedAt)) {
		target.Catalog = provisional.Catalog
	}

	return mergeTerminalObservations(target, provisional)
}

func mergeTerminalObservations(target Observations, provisional Observations) Observations {
	if target.Location == nil ||
		(provisional.Location != nil && provisional.Location.ObservedAt.After(target.Location.ObservedAt)) {
		target.Location = provisional.Location
	}

	if target.Screen == nil ||
		(provisional.Screen != nil && provisional.Screen.ObservedAt.After(target.Screen.ObservedAt)) {
		target.Screen = provisional.Screen
	}

	return target
}
