package registry

import (
	"path/filepath"
	"time"
)

func reconcileResumedProcessSession(sessions map[string]Session, session *Session, observation Observation) {
	for id, provisional := range sessions {
		if id == session.ID || provisional.Harness != session.Harness || provisional.IdentityState != Provisional || provisional.Observations.Native != nil || provisional.Process == nil || !provisional.Process.Equal(*observation.ProcessIdentity()) {
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

		session.Incarnation.ObservedAt = maxTime(session.Incarnation.ObservedAt, provisional.Incarnation.ObservedAt)
		delete(sessions, id)
	}
}

func (r Reducer) retireConflictingProcessSessions(
	sessions map[string]Session,
	observation Observation,
	at time.Time,
	receivedAt time.Time,
) {
	if observation.ProcessIdentity() == nil || !observation.ProcessIdentity().Complete() {
		return
	}

	for id, session := range sessions {
		replace, nativeReplacement := r.conflictingSessionReplacement(session, observation)
		if !replace {
			continue
		}
		if currentAt := session.Incarnation.ObservedAt; !currentAt.IsZero() && at.Before(currentAt) {
			continue
		}
		if session.Presence() != PresenceGone {
			(lifecycleMachine{session: &session}).end(at)
			if nativeReplacement {
				session.setDecision(&ActivityDecision{
					Authority: "hook", Reason: "session_replaced", RuleID: "", ManifestSource: "",
					ManifestVersion: 0, FallbackReason: "", Process: *observation.ProcessIdentity(), ObservedAt: at,
				})
			}
		}
		session.UpdatedAt = maxTime(session.UpdatedAt, receivedAt)
		sessions[id] = session
	}
}

func (r Reducer) conflictingSessionReplacement(session Session, observation Observation) (bool, bool) {
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
	if session.Process == nil && observation.Kind() == "placement" && samePane(session, observation.Location()) {
		return true, false
	}
	return false, false
}

func (r Reducer) nativeSessionReplacement(session Session, observation Observation, sameProcess bool) bool {
	if !sameProcess ||
		observation.Kind() != "report" ||
		!observationHasIdentity(observation) ||
		session.Harness != observation.Harness ||
		!observationIdentityConflicts(session, observation.Subject) {
		return false
	}
	if observation.Report().Reporter.MultiSession || (session.Observations.Native != nil && session.Observations.Native.Reporter.MultiSession) {
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

//nolint:cyclop // matching session discovery correlates native and provisional identities
func findAndReconcileMatchingSession(sessions map[string]Session, observation Observation, at time.Time) string {
	identityID := findIdentityMatchingSession(sessions, observation)
	processID := findProcessMatchingSession(sessions, observation)
	if observation.Kind() == "report" && identityID != "" && processID != "" &&
		identityID != processID && sessions[identityID].Process != nil &&
		sessions[identityID].Presence() == PresenceGone && sessions[processID].IdentityState == Provisional && sessions[processID].Observations.Native == nil {
		return identityID
	}
	if identityID != "" && processID != "" && identityID != processID {
		if prior := sessions[identityID]; prior.Process != nil && !prior.Process.Equal(*observation.ProcessIdentity()) {
			// The prior process terminated and the same session is being resumed
			// or restarted under a new process. Retire the stale incarnation so
			// the registry never retains two live sessions for one session_id.
			(lifecycleMachine{session: &prior}).end(at)
			prior.UpdatedAt = maxTime(prior.UpdatedAt, at)
			sessions[identityID] = prior
		}
		provisional := sessions[identityID]
		if provisional.Process == nil {
			target := mergeProvisionalSession(sessions[processID], provisional)
			sessions[processID] = target
			delete(sessions, identityID)
			return processID
		}
	}
	if processID != "" {
		return processID
	}
	if identityID != "" {
		return identityID
	}
	if observation.Kind() == "listing" && observation.Listing() != nil && observation.Listing().ProcessPID > 0 {
		return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.Process != nil && session.Process.PID == observation.Listing().ProcessPID
		})
	}
	return ""
}

func findMatchingSession(sessions map[string]Session, observation Observation) string {
	if id := findProcessMatchingSession(sessions, observation); id != "" {
		return id
	}
	if id := findIdentityMatchingSession(sessions, observation); id != "" {
		return id
	}
	if observation.Kind() == "listing" && observation.Listing() != nil && observation.Listing().ProcessPID > 0 {
		return bestMatchingSessionID(sessions, observation.Harness, func(session Session) bool {
			return session.Process != nil && session.Process.PID == observation.Listing().ProcessPID
		})
	}
	return ""
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

func bestMatchingSessionID(sessions map[string]Session, harness Harness, match func(Session) bool) string {
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

func sessionMatchPreferred(candidate Session, candidateID string, current Session, currentID string) bool {
	if candidateRank := presenceMatchRank(candidate.Presence()); candidateRank != presenceMatchRank(current.Presence()) {
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

//nolint:cyclop // each independent metadata dimension is merged only when missing
func mergeProvisionalSession(target Session, provisional Session) Session {
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

	if target.Presence() == PresenceUnknown && provisional.Presence() != PresenceUnknown {
		if _, gone := provisional.Liveness.(Gone); gone {
			target.Liveness = provisional.Liveness
		} else {
			target.setPresence(provisional.Presence())
		}
		target.PresenceChangedAt = provisional.PresenceChangedAt
	}
	if activityUnknown(target.Activity()) && !activityUnknown(provisional.Activity()) {
		target.setActivity(clonePtr(provisional.Activity()))
		target.ActivityChangedAt = provisional.ActivityChangedAt
		target.setDecision(provisional.Decision())
	}
	target.Observations = mergeObservations(target.Observations, provisional.Observations)
	if target.CreatedAt.IsZero() || (!provisional.CreatedAt.IsZero() && provisional.CreatedAt.Before(target.CreatedAt)) {
		target.CreatedAt = provisional.CreatedAt
	}
	target.UpdatedAt = maxTime(target.UpdatedAt, provisional.UpdatedAt)
	if target.SessionID != "" || target.SessionPath != "" {
		target.IdentityState = Identified
	}
	target.Incarnation = incarnationFromEvidence(target)
	return target
}

func activityUnknown(activity *Activity) bool {
	return activity == nil || *activity == ActivityUnknown
}

//nolint:cyclop // each evidence source has an independent newest-observation slot
func mergeObservations(target Observations, provisional Observations) Observations {
	if target.Native == nil || (provisional.Native != nil && provisional.Native.ObservedAt.After(target.Native.ObservedAt)) {
		target.Native = provisional.Native
	}
	if target.Process == nil || (provisional.Process != nil && provisional.Process.ObservedAt.After(target.Process.ObservedAt)) {
		target.Process = provisional.Process
	}
	if target.Location == nil || (provisional.Location != nil && provisional.Location.ObservedAt.After(target.Location.ObservedAt)) {
		target.Location = provisional.Location
	}

	if target.Catalog == nil || (provisional.Catalog != nil && provisional.Catalog.ObservedAt.After(target.Catalog.ObservedAt)) {
		target.Catalog = provisional.Catalog
	}
	if target.Screen == nil || (provisional.Screen != nil && provisional.Screen.ObservedAt.After(target.Screen.ObservedAt)) {
		target.Screen = provisional.Screen
	}
	return target
}
