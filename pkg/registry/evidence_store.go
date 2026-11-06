package registry

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

func observationTime(observedAt time.Time, receivedAt time.Time) time.Time {
	observedAt = observedAt.UTC()
	if observedAt.After(receivedAt) {
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
	case *Verification, nil:
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
	case *Verification, nil:
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
		!sameReportProcess(previous, observation.Report()) ||
		strings.TrimSpace(previous.Reporter.Integration) != reporter {
		return at, nil
	}

	if duplicateSequencedReport(session, observation) {
		return previous.ObservedAt, nil
	}

	if err := validateSequenceOrder(
		reporter,
		previous.Reporter.Sequence,
		observation.Report().Reporter.Sequence,
	); err != nil {
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

func duplicateSequencedReport(session Session, observation Observation) bool {
	report := observation.Report()
	previous := session.Observations.Native

	return report != nil && previous != nil && sameReportProcess(previous, report) &&
		report.Reporter.Sequence != nil &&
		previous.Reporter.Sequence != nil &&
		*report.Reporter.Sequence == *previous.Reporter.Sequence &&
		strings.TrimSpace(report.Reporter.Integration) != "" &&
		strings.TrimSpace(
			report.Reporter.Integration,
		) == strings.TrimSpace(
			previous.Reporter.Integration,
		) &&
		observationEquivalent(session, observation, previous.ObservedAt)
}

func validateSequenceOrder(reporter string, previous *uint64, current *uint64) error {
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

	return reflect.DeepEqual(
		existingSlot(session, observation),
		existingSlot(candidate, observation),
	)
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
	case *Verification, nil:
	}
}

func storeNativeObservation(session *Session, observation Observation, at time.Time) {
	var process ProcessIdentity
	if observation.ProcessIdentity() != nil {
		process = *observation.ProcessIdentity()
	}

	reporter := observation.Report().Reporter
	reporter.Sequence = clonePtr(reporter.Sequence)
	detail := session.nativeDetail(observation.Report(), process, at)
	session.Observations.Native = &NativeObservation{
		Reporter:    reporter,
		Event:       observation.Report().Event,
		Lifecycle:   clonePtr(observation.Report().Lifecycle),
		Presence:    clonePtr(observation.Report().Claim),
		Activity:    session.nativeActivity(observation.Report(), process),
		SessionID:   observation.Subject.SessionID,
		SessionPath: observation.Subject.SessionPath,
		ObservedAt:  at,
		Attributes:  cloneAttributes(observation.Report().Attributes),
		RawPayload:  cloneRaw(observation.Report().Payload),
		Process:     process,
		Detail:      detail,
	}
}

func (s Session) nativeActivity(report *Report, process ProcessIdentity) *Activity {
	activity := report.Activity

	previous := s.Observations.Native
	if activity == nil && s.Presence() != PresenceGone && !reportEnds(report) &&
		previous != nil && !nativeEnded(previous) && previous.Reporter.Integration == report.Reporter.Integration &&
		s.Process != nil && s.Process.Equal(process) && previous.Process.Equal(process) {
		activity = previous.Activity
	}

	return clonePtr(activity)
}

func storeProcessObservation(session *Session, observation Observation, at time.Time) {
	present := observation.Present() != nil && *observation.Present()

	var process ProcessIdentity
	if observation.ProcessIdentity() != nil {
		process = *observation.ProcessIdentity()
	}

	session.Observations.Process = &ProcessObservation{
		Present:    present,
		Process:    process,
		ObservedAt: at,
	}
}

func storeMultiplexerObservation(session *Session, observation Observation, at time.Time) {
	session.Observations.Location = &MultiplexerObservation{
		Process:    *observation.ProcessIdentity(),
		Location:   *observation.Location(),
		ObservedAt: at,
	}
}

func storeCatalogObservation(session *Session, observation Observation, at time.Time) {
	session.Observations.Catalog = &CatalogObservation{
		SessionID:     observation.Subject.SessionID,
		SessionPath:   observation.Subject.SessionPath,
		ResumeCommand: append([]string(nil), observation.Listing().ResumeCommand...),
		CWD:           observation.Listing().CWD,
		ProjectRoot:   observation.Listing().ProjectRoot,
		ProcessPID:    observation.Listing().ProcessPID,
		ObservedAt:    at,
	}
}

func storeScreenObservation(session *Session, observation Observation, at time.Time) {
	screen := *observation.Reading()
	screen.Activity = *observation.ActivityClaim()
	screen.Process = *observation.ProcessIdentity()
	screen.ObservedAt = at
	session.Observations.Screen = &screen
}

func sameReportProcess(previous *NativeObservation, report *Report) bool {
	if report.Process == nil {
		return true
	}

	return previous.Process.Equal(*report.Process)
}
