package registry

import (
	"fmt"
	"strings"
)

func (o Observation) Validate(rules Rules) error {
	if o.Harness == "" {
		return fmt.Errorf("%w: %w", ErrInvalidObservation, ErrHarnessRequired)
	}
	if !rules.Known(o.Harness) {
		return fmt.Errorf("%w: harness %q is not canonical", ErrInvalidObservation, o.Harness)
	}
	if o.At.IsZero() {
		return fmt.Errorf("%w: at is required", ErrInvalidObservation)
	}
	if err := validateEvidence(o.Evidence); err != nil {
		return err
	}
	if process := o.ProcessIdentity(); process != nil && !validStoredProcess(*process, false) {
		return fmt.Errorf("%w: invalid process identity", ErrInvalidObservation)
	}
	if o.Subject.SessionID == "" && o.Subject.SessionPath == "" && o.ProcessIdentity() == nil {
		return fmt.Errorf("%w: identity is required", ErrInvalidObservation)
	}
	return nil
}

func validateEvidence(evidence Evidence) error {
	if evidence == nil {
		return fmt.Errorf("%w: evidence is required", ErrInvalidObservation)
	}
	return evidence.validate()
}

func (r *Report) validate() error {
	if r == nil {
		return fmt.Errorf("%w: report is required", ErrInvalidObservation)
	}
	return validateReport(*r)
}

func (s *Sighting) validate() error {
	if s == nil || (s.Present && !s.Process.Complete()) {
		return fmt.Errorf("%w: incomplete sighting", ErrInvalidObservation)
	}
	return nil
}

func (p *Placement) validate() error {
	if p == nil || !p.Process.Complete() {
		return fmt.Errorf("%w: incomplete placement", ErrInvalidObservation)
	}
	return validateLocation(&p.Location)
}

func (l *Listing) validate() error {
	if l == nil {
		return fmt.Errorf("%w: listing is required", ErrInvalidObservation)
	}
	return validateListing(l)
}

func (r *Reading) validate() error {
	if r == nil || !r.Process.Complete() || !r.Activity.IsValid() {
		return fmt.Errorf("%w: incomplete reading", ErrInvalidObservation)
	}
	return nil
}

func validateReport(report Report) error {
	if !validStoredLifecycle(report.Lifecycle) || !validStoredOptionalPresence(report.Claim) || !validStoredOptionalActivity(report.Activity) {
		return fmt.Errorf("%w: invalid report state", ErrInvalidObservation)
	}
	if report.Lifecycle != nil && *report.Lifecycle == NativeLifecycleEnd && report.Activity != nil {
		return fmt.Errorf("%w: end cannot include activity", ErrInvalidObservation)
	}
	if report.Lifecycle == nil && report.Claim == nil && report.Activity == nil && report.Event == "" {
		return fmt.Errorf("%w: event or transition is required", ErrInvalidObservation)
	}
	return validateReportMetadata(report)
}

func validateReportMetadata(report Report) error {
	if report.Reporter.Version < 0 {
		return fmt.Errorf("%w: reporter version must not be negative", ErrInvalidObservation)
	}
	if report.Reporter.Sequence != nil && strings.TrimSpace(report.Reporter.Integration) == "" {
		return fmt.Errorf("%w: sequenced report requires reporter", ErrInvalidObservation)
	}
	if err := validateLocation(report.Location); err != nil {
		return err
	}
	return validateListing(report.Listing)
}

func validateLocation(location *Location) error {
	if location == nil {
		return nil
	}
	if location.PanePID < 0 || (!location.Empty() && !location.Kind.IsValid()) {
		return fmt.Errorf("%w: invalid location", ErrInvalidObservation)
	}
	return nil
}

func validateListing(listing *Listing) error {
	if listing != nil && listing.ProcessPID < 0 {
		return fmt.Errorf("%w: invalid listing process pid", ErrInvalidObservation)
	}
	return nil
}
