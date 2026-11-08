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
	if observation.Evidence == nil {
		return time.Time{}
	}

	_, at := observation.Evidence.slot(session)

	return at
}

func existingSlot(session Session, observation Observation) any {
	if observation.Evidence == nil {
		return nil
	}

	slot, _ := observation.Evidence.slot(session)

	return slot
}

func (r *Report) slot(s Session) (any, time.Time) {
	if s.Observations.Native == nil {
		return nil, time.Time{}
	}

	return s.Observations.Native, s.Observations.Native.ObservedAt
}

func (r *Sighting) slot(s Session) (any, time.Time) {
	if s.Observations.Process == nil {
		return nil, time.Time{}
	}

	return s.Observations.Process, s.Observations.Process.ObservedAt
}

func (r *Placement) slot(s Session) (any, time.Time) {
	if s.Observations.Location == nil {
		return nil, time.Time{}
	}

	return s.Observations.Location, s.Observations.Location.ObservedAt
}

func (r *Listing) slot(s Session) (any, time.Time) {
	if s.Observations.Catalog == nil {
		return nil, time.Time{}
	}

	return s.Observations.Catalog, s.Observations.Catalog.ObservedAt
}

func (r *Reading) slot(s Session) (any, time.Time) {
	if s.Observations.Screen == nil {
		return nil, time.Time{}
	}

	return s.Observations.Screen, s.Observations.Screen.ObservedAt
}
func (*Verification) slot(Session) (any, time.Time) { return nil, time.Time{} }

func sequencedObservationTime(
	session Session,
	observation Observation,
	at time.Time,
) (time.Time, error) {
	if observation.Kind() != EvidenceReport {
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

func storeObservation(session *Session, observation Observation, at time.Time) {
	if observation.Evidence != nil {
		observation.Evidence.store(session, observation.Subject, at)
	}
}

func (r *Report) store(session *Session, subject ObservationIdentity, at time.Time) {
	var process ProcessIdentity
	if r.Process != nil {
		process = *r.Process
	}

	native := r.NativeEvidence.clone()
	native.Activity = session.nativeActivity(r, process)
	detail := session.nativeDetail(r, process, at)
	session.Observations.Native = &NativeObservation{
		NativeEvidence: native, Presence: clonePtr(r.Claim),

		SessionID:   subject.SessionID,
		SessionPath: subject.SessionPath,
		ObservedAt:  at,

		RawPayload: cloneRaw(r.Payload),
		Process:    process,
		Detail:     detail,
	}
}

func (s Session) nativeActivity(report *Report, process ProcessIdentity) *Activity {
	activity := report.Activity

	previous := s.Observations.Native
	if activity == nil && s.Presence() != PresenceGone && !report.Ends() &&
		previous != nil && !nativeEnded(previous) && previous.Reporter.Integration == report.Reporter.Integration &&
		s.Process != nil && s.Process.Equal(process) && previous.Process.Equal(process) {
		activity = previous.Activity
	}

	return clonePtr(activity)
}

func (r *Sighting) store(session *Session, _ ObservationIdentity, at time.Time) {
	session.Observations.Process = &ProcessObservation{Sighting: *r, ObservedAt: at}
}

func (r *Placement) store(session *Session, _ ObservationIdentity, at time.Time) {
	session.Observations.Location = &MultiplexerObservation{
		Placement:  *r,
		ObservedAt: at,
	}
}

func (r *Listing) store(session *Session, subject ObservationIdentity, at time.Time) {
	session.Observations.Catalog = &CatalogObservation{
		ListingMetadata: r.ListingMetadata.clone(),
		SessionID:       subject.SessionID,
		SessionPath:     subject.SessionPath,
		ObservedAt:      at,
	}
}

func (r *Reading) store(session *Session, _ ObservationIdentity, at time.Time) {
	screen := ScreenObservation(*r)
	screen.ObservedAt = at
	session.Observations.Screen = &screen
}
func (*Verification) store(*Session, ObservationIdentity, time.Time) {}

func sameReportProcess(previous *NativeObservation, report *Report) bool {
	if report.Process == nil {
		return true
	}

	return previous.Process.Equal(*report.Process)
}
