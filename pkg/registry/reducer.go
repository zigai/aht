package registry

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

func (r Reducer) applyObservationBatch(ctx context.Context, snap *snapshot, observations []Observation, receivedAt time.Time) ([]Session, error) {
	saved := make([]Session, 0, len(observations))
	for index := range observations {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("checking context: %w", err)
		}

		observation := observations[index]
		if observation.At.IsZero() {
			observation.At = receivedAt
		}
		if err := observation.Validate(r.rules); err != nil {
			return nil, err
		}

		session, keep, err := r.reduceOne(snap.Sessions, observation, receivedAt)
		if err != nil {
			return nil, err
		}
		if keep {
			saved = append(saved, session)
		}
	}

	snap.UpdatedAt = maxTime(snap.UpdatedAt, receivedAt)

	return saved, nil
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
		Incarnation: incarnation, IdentityState: Provisional, SchemaVersion: storeSchemaVersion,
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
		session.IdentityState = Identified
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
// identity. No native report can match them and a process start identity never
// recurs, so their tombstones protect nothing. Removal runs after a whole
// batch, so matching within the batch still sees them.
func removeGoneProcessOnlySessions(sessions map[string]Session) int {
	removed := 0
	for id, session := range sessions {
		if session.Presence() == PresenceGone && processOnlySession(session) {
			delete(sessions, id)
			removed++
		}
	}
	return removed
}

func hasExpiredTombstones(sessions map[string]Session, now time.Time, ttl time.Duration) bool {
	for _, session := range sessions {
		if session.Presence() == PresenceGone && (processOnlySession(session) || now.Sub(goneSince(session)) >= ttl) {
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
// sessions that have been gone for at least ttl. The ttl window keeps late
// native reports from an ended incarnation from reviving the session.
func expireTombstones(sessions map[string]Session, now time.Time, ttl time.Duration) int {
	removed := removeGoneProcessOnlySessions(sessions)
	for id, session := range sessions {
		if session.Presence() != PresenceGone {
			continue
		}
		if now.Sub(goneSince(session)) < ttl {
			continue
		}
		delete(sessions, id)
		removed++
	}
	return removed
}

func processOnlySession(session Session) bool {
	return session.IdentityState == Provisional && session.Observations.Native == nil && session.SessionID == "" && session.SessionPath == ""
}
