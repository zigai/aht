package registry

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

// applyObservationBatch reduces observations into snap and returns the saved
// sessions and the number of reports dropped because their process ended.
func (r Reducer) applyObservationBatch(ctx context.Context, snap *snapshot, observations []Observation, receivedAt time.Time) ([]Session, int, error) {
	saved := make([]Session, 0, len(observations))
	ended := 0
	for index := range observations {
		if err := ctx.Err(); err != nil {
			return nil, 0, fmt.Errorf("checking context: %w", err)
		}

		observation := observations[index]
		if observation.At.IsZero() {
			observation.At = receivedAt
		}
		if err := observation.Validate(r.rules); err != nil {
			return nil, 0, err
		}
		if endedProcessReport(*snap, observation) {
			ended++
			continue
		}

		session, keep, err := r.reduceOne(snap.Sessions, observation, receivedAt)
		if err != nil {
			return nil, 0, err
		}
		if keep {
			saved = append(saved, session)
		}
	}

	snap.UpdatedAt = maxTime(snap.UpdatedAt, receivedAt)

	return saved, ended, nil
}

func (r Reducer) reduceOne(sessions map[string]Session, observation Observation, receivedAt time.Time) (Session, bool, error) {
	var empty Session
	at := observationTime(observation.At, receivedAt)
	r.retireConflictingProcessSessions(sessions, observation, at, receivedAt)
	id := findAndReconcileMatchingSession(sessions, observation, at)
	if id == "" && observation.Kind() == "listing" && (!r.rules.Policy(observation.Harness).CatalogCreates || !observation.Listing().Current) {
		return empty, false, nil
	}
	if id == "" {
		id = sessionIDForObservation(observation)
	}
	session := sessions[id]
	if session.ID != "" && session.Harness != observation.Harness {
		id = sessionIDForObservation(observation)
		session = empty
	}
	if session.ID == "" {
		session = newSession(id, observation.Harness, receivedAt)
	}
	session, err := r.reduceSession(sessions, session, observation, at, receivedAt)
	if err != nil {
		return Session{}, false, err
	}
	sessions[session.ID] = session
	return session, true, nil
}

func (r Reducer) reduceSession(sessions map[string]Session, session Session, observation Observation, at, receivedAt time.Time) (Session, error) {
	at, err := sequencedObservationTime(session, observation, at)
	if err != nil {
		return Session{}, err
	}
	resume := beginsIncarnation(session, observation, at)
	if !acceptsReport(session, observation, at) {
		return session, nil
	}
	if err := r.applyObservation(&session, observation, at, receivedAt); err != nil {
		return Session{}, err
	}
	if resume {
		reconcileResumedProcessSession(sessions, &session, observation)
	}
	return session, nil
}

func newSession(id string, harness Harness, now time.Time) Session {
	var location Location
	var observations Observations
	var incarnation Incarnation
	activity := ActivityUnknown
	return Session{
		Incarnation: incarnation, IdentityState: IdentityProvisional, SchemaVersion: storeSchemaVersion,
		ID:            id,
		Harness:       harness,
		SessionID:     "",
		SessionPath:   "",
		ResumeCommand: nil,
		CWD:           "",
		ProjectRoot:   "",
		Process:       nil,
		Location:      location,

		Observations:      observations,
		CreatedAt:         now,
		UpdatedAt:         now,
		PresenceChangedAt: time.Time{},
		ActivityChangedAt: time.Time{},
		Liveness:          NewLiveness(PresenceUnknown, ActivityValue(&activity), nil),
		Detail:            nil,
	}
}

func (r Reducer) applyObservation(session *Session, observation Observation, at, receivedAt time.Time) error {
	if err := validateIncomingProcessTime(*session, observation, at); err != nil {
		return err
	}
	if previous := sourceSlotTime(*session, observation); !previous.IsZero() {
		if at.Before(previous) {
			return fmt.Errorf("%w: %s observation at %s precedes %s", ErrObservationConflict, observation.Kind(), at, previous)
		}
		if at.Equal(previous) {
			if observationEquivalent(*session, observation, at) {
				return nil
			}
			return fmt.Errorf("%w: %s observation at %s", ErrObservationConflict, observation.Kind(), at)
		}
	}
	previousPresence := session.Presence()
	previousActivity := session.Activity()
	resumesIncarnation := beginsIncarnation(*session, observation, at)
	storeObservation(session, observation, at)
	applyIdentity(session, observation)
	applyMetadata(session, observation, at)
	if resumesIncarnation {
		(lifecycleMachine{session: session}).resume()
	}
	machine := lifecycleMachine{session: session}
	machine.apply(observation, r.rules.Policy(session.Harness), at)
	session.resolveDetail(r.rules.Policy(session.Harness), receivedAt)
	session.SchemaVersion = storeSchemaVersion
	session.UpdatedAt = maxTime(session.UpdatedAt, receivedAt)
	if session.Presence() != previousPresence {
		session.PresenceChangedAt = at
	}
	if !activityEqual(session.Activity(), previousActivity) {
		session.ActivityChangedAt = at
	}
	return nil
}

func validateIncomingProcessTime(session Session, observation Observation, at time.Time) error {
	if observation.ProcessIdentity() == nil || session.Process == nil || session.Process.Equal(*observation.ProcessIdentity()) {
		return nil
	}
	currentAt := session.Incarnation.ObservedAt
	if currentAt.IsZero() || !at.Before(currentAt) {
		return nil
	}
	return fmt.Errorf("%w: process identity observation at %s precedes current process at %s", ErrObservationConflict, at, currentAt)
}

func applyIdentity(session *Session, observation Observation) {
	if observationHasIdentity(observation) {
		session.IdentityState = IdentityIdentified
	}
	if observation.Subject.SessionID != "" {
		session.SessionID = observation.Subject.SessionID
	}
	if observation.Subject.SessionPath != "" {
		session.SessionPath = filepath.Clean(observation.Subject.SessionPath)
	}
}

func applyMetadata(session *Session, observation Observation, at time.Time) {
	if observation.ProcessIdentity() != nil && observation.ProcessIdentity().Complete() {
		process := *observation.ProcessIdentity()
		if session.Process != nil && !session.Process.Equal(process) {
			(lifecycleMachine{session: session}).processReplaced(process, at)
		}
		session.Process = &process
		session.Incarnation.ObservedAt = maxTime(session.Incarnation.ObservedAt, at)
		if process.CWD != "" {
			session.CWD = process.CWD
		}
		if observation.Kind() == "sighting" {
			return
		}
	}
	if (observation.Kind() == "report" || observation.Kind() == "listing") && observation.Listing() != nil {
		applyListing(session, observation.Listing())
	}
	if observation.Location() != nil {
		session.Location = *observation.Location()

		if session.CWD == "" {
			session.CWD = observation.Location().PaneCurrentPath
		}
	}
}

func applyListing(session *Session, catalog *Listing) {
	if session.CWD == "" {
		session.CWD = catalog.CWD
	}
	if session.ProjectRoot == "" {
		session.ProjectRoot = catalog.ProjectRoot
	}
	if len(session.ResumeCommand) == 0 {
		session.ResumeCommand = append([]string(nil), catalog.ResumeCommand...)
	}
}

func deleteExpiredGoneSessions(
	sessions map[string]Session,
	now time.Time,
	deleteAfter time.Duration,
	changedAt func(Session) time.Time,
) int {
	if deleteAfter < 0 {
		return 0
	}

	deleted := 0
	for id, session := range sessions {
		at := changedAt(session)
		if session.Presence() != PresenceGone || at.IsZero() || now.Sub(at) < deleteAfter {
			continue
		}

		delete(sessions, id)
		deleted++
	}

	return deleted
}

// removeGoneProcessOnlySessions drops gone sessions that have only process
// identity and records their processes as ended. No native report can match
// them and a process start identity never recurs, so their tombstones protect
// nothing. Removal runs after a whole batch, so matching within the batch
// still sees them.
func removeGoneProcessOnlySessions(snap *snapshot) int {
	removed := 0
	for id, session := range snap.Sessions {
		if session.Presence() != PresenceGone || !processOnlySession(session) {
			continue
		}
		if session.Process != nil && session.Process.Complete() {
			if snap.EndedProcesses == nil {
				snap.EndedProcesses = make(map[string]time.Time)
			}
			snap.EndedProcesses[endedProcessKey(*session.Process)] = goneSince(session)
		}
		delete(snap.Sessions, id)
		removed++
	}
	return removed
}

func endedProcessKey(process ProcessIdentity) string {
	return strconv.Itoa(process.PID) + ":" + process.StartIdentity
}

// endedProcessReport reports whether observation is a native report from an
// ended process that no remaining session matches. Such a report arrived after
// its process-only session was removed and would otherwise recreate it.
func endedProcessReport(snap snapshot, observation Observation) bool {
	process := observation.ProcessIdentity()
	if observation.Kind() != "report" || process == nil || !process.Complete() {
		return false
	}
	if _, ended := snap.EndedProcesses[endedProcessKey(*process)]; !ended {
		return false
	}
	return findMatchingSession(snap.Sessions, observation) == ""
}

// pruneEndedProcesses forgets ended processes recorded at least ttl before now.
func pruneEndedProcesses(snap *snapshot, now time.Time, ttl time.Duration) {
	for key, at := range snap.EndedProcesses {
		if now.Sub(at) >= ttl {
			delete(snap.EndedProcesses, key)
		}
	}
	if len(snap.EndedProcesses) == 0 {
		snap.EndedProcesses = nil
	}
}

func hasExpiredTombstones(snap snapshot, now time.Time, ttl time.Duration) bool {
	for _, session := range snap.Sessions {
		if session.Presence() == PresenceGone && (processOnlySession(session) || now.Sub(goneSince(session)) >= ttl) {
			return true
		}
	}
	for _, at := range snap.EndedProcesses {
		if now.Sub(at) >= ttl {
			return true
		}
	}
	return false
}

func goneSince(session Session) time.Time {
	if session.PresenceChangedAt.IsZero() {
		return session.UpdatedAt
	}
	return session.PresenceChangedAt
}

// expireTombstones removes gone process-only sessions and identified gone
// sessions that have been gone for at least ttl, and forgets ended processes
// after the same ttl. The ttl window keeps late native reports from an ended
// incarnation from reviving the session. It returns the sessions removed.
func expireTombstones(snap *snapshot, now time.Time, ttl time.Duration) int {
	removed := removeGoneProcessOnlySessions(snap)
	for id, session := range snap.Sessions {
		if session.Presence() != PresenceGone {
			continue
		}
		if now.Sub(goneSince(session)) < ttl {
			continue
		}
		delete(snap.Sessions, id)
		removed++
	}
	pruneEndedProcesses(snap, now, ttl)
	return removed
}

func processOnlySession(session Session) bool {
	return session.IdentityState == IdentityProvisional && session.Observations.Native == nil && session.SessionID == "" && session.SessionPath == ""
}
