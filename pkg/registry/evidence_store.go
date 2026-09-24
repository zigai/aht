package registry

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

func observationTime(observedAt, receivedAt time.Time) time.Time {
	observedAt = observedAt.UTC()
	if observedAt.After(receivedAt.Add(maxObservedAtFutureSkew)) {
		return receivedAt
	}
	return observedAt
}

func sourceSlotTime(session Session, observation Observation) time.Time {
	switch observation.Evidence.(type) {
	case *Report:
		if session.Observations.Native != nil {
			return session.Observations.Native.ObservedAt
		}
	case *Sighting:
		if session.Observations.Process != nil {
			return session.Observations.Process.ObservedAt
		}
	case *Placement:
		if session.Observations.Location != nil {
			return session.Observations.Location.ObservedAt
		}
	case *Listing:
		if session.Observations.Catalog != nil {
			return session.Observations.Catalog.ObservedAt
		}
	case *Reading:
		if session.Observations.Screen != nil {
			return session.Observations.Screen.ObservedAt
		}
	}
	return time.Time{}
}

func existingSlot(session Session, observation Observation) any {
	switch observation.Evidence.(type) {
	case *Report:
		return session.Observations.Native
	case *Sighting:
		return session.Observations.Process
	case *Placement:
		return session.Observations.Location
	case *Listing:
		return session.Observations.Catalog
	case *Reading:
		return session.Observations.Screen
	case nil:
		return nil
	}
	return nil
}

func sequencedObservationTime(
	session Session,
	observation Observation,
	at time.Time,
) (time.Time, error) {
	if observation.Kind() != "report" {
		return at, nil
	}

	reporter := strings.TrimSpace(observation.Report().Reporter.Integration)
	previous := session.Observations.Native
	if reporter == "" ||
		previous == nil ||
		strings.TrimSpace(previous.Reporter.Integration) != reporter {
		return at, nil
	}
	if err := validateSequenceOrder(reporter, previous.Reporter.Sequence, observation.Report().Reporter.Sequence); err != nil {
		return time.Time{}, err
	}
	if observation.Report().Reporter.Sequence == nil {
		if at.Equal(previous.ObservedAt) && !observationEquivalent(session, observation, at) {
			at = previous.ObservedAt.Add(time.Nanosecond)
		}
		return at, nil
	}
	if !at.After(previous.ObservedAt) {
		at = previous.ObservedAt.Add(time.Nanosecond)
	}

	return at, nil
}

func validateSequenceOrder(reporter string, previous, current *uint64) error {
	if previous != nil && current == nil {
		return fmt.Errorf(
			"%w: reporter %q omitted sequence after using sequence %d",
			ErrObservationConflict,
			reporter,
			*previous,
		)
	}
	if previous != nil && current != nil && *current <= *previous {
		return fmt.Errorf(
			"%w: reporter %q sequence %d does not follow %d",
			ErrObservationConflict,
			reporter,
			*current,
			*previous,
		)
	}
	return nil
}

func observationEquivalent(session Session, observation Observation, at time.Time) bool {
	candidate := session
	storeObservation(&candidate, observation, at)
	return reflect.DeepEqual(existingSlot(session, observation), existingSlot(candidate, observation))
}

// storeObservation records an observation in the appropriate slot.
// Precondition: observation has been validated via Observation.Validate.
func storeObservation(session *Session, observation Observation, at time.Time) {
	switch observation.Evidence.(type) {
	case *Report:
		storeNativeObservation(session, observation, at)
	case *Sighting:
		storeProcessObservation(session, observation, at)
	case *Placement:
		storeMultiplexerObservation(session, observation, at)
	case *Listing:
		storeCatalogObservation(session, observation, at)
	case *Reading:
		storeScreenObservation(session, observation, at)
	}
}

func storeNativeObservation(session *Session, observation Observation, at time.Time) {
	var process ProcessIdentity
	if observation.ProcessIdentity() != nil {
		process = *observation.ProcessIdentity()
	}
	session.Incarnation.NativeEnded = observation.Report().Lifecycle != nil && *observation.Report().Lifecycle == NativeLifecycleEnd
	if process.Complete() {
		session.Incarnation.NativeProcess = process
	}
	reporter := observation.Report().Reporter
	reporter.Sequence = clonePtr(reporter.Sequence)
	session.Observations.Native = &NativeObservation{Reporter: reporter, Event: observation.Report().Event, Lifecycle: clonePtr(observation.Report().Lifecycle), Presence: clonePtr(observation.Report().Claim), Activity: clonePtr(observation.ActivityClaim()), SessionID: observation.Subject.SessionID, SessionPath: observation.Subject.SessionPath, ObservedAt: at, Attributes: cloneAttributes(observation.Report().Attributes), RawPayload: cloneRaw(observation.Report().Payload), Process: process}
}

func storeProcessObservation(session *Session, observation Observation, at time.Time) {
	present := observation.Present() != nil && *observation.Present()
	var process ProcessIdentity
	if observation.ProcessIdentity() != nil {
		process = *observation.ProcessIdentity()
	}
	session.Observations.Process = &ProcessObservation{Present: present, Process: process, ObservedAt: at}
}

func storeMultiplexerObservation(session *Session, observation Observation, at time.Time) {
	session.Observations.Location = &MultiplexerObservation{Process: *observation.ProcessIdentity(), Context: *observation.Location(), ObservedAt: at}
}

func storeCatalogObservation(session *Session, observation Observation, at time.Time) {
	session.Observations.Catalog = &CatalogObservation{SessionID: observation.Subject.SessionID, SessionPath: observation.Subject.SessionPath, ResumeCommand: append([]string(nil), observation.Listing().ResumeCommand...), CWD: observation.Listing().CWD, ProjectRoot: observation.Listing().ProjectRoot, ProcessPID: observation.Listing().ProcessPID, ObservedAt: at}
}

func storeScreenObservation(session *Session, observation Observation, at time.Time) {
	screen := *observation.Reading()
	screen.Activity = *observation.ActivityClaim()
	screen.Process = *observation.ProcessIdentity()
	screen.ObservedAt = at
	session.Observations.Screen = &screen
}
