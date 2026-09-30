package registry

import (
	"fmt"
	"strings"
	"time"
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
	if err := validateEvidenceAt(o.Evidence, o.At); err != nil {
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

func validateEvidenceAt(evidence Evidence, at time.Time) error {
	if evidence == nil {
		return fmt.Errorf("%w: evidence is required", ErrInvalidObservation)
	}
	if err := evidence.validate(); err != nil {
		return err
	}
	if report, ok := evidence.(*Report); ok && report.DetailObservedAt != nil && report.DetailObservedAt.After(at) {
		return fmt.Errorf("%w: detail timestamp follows report", ErrInvalidObservation)
	}
	return nil
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
	if r == nil || !r.Process.Complete() || !r.Activity.Valid() {
		return fmt.Errorf("%w: incomplete reading", ErrInvalidObservation)
	}
	if r.Detail != "" && !r.Detail.ValidFor(r.Activity) {
		return fmt.Errorf("%w: invalid reading detail", ErrInvalidObservation)
	}
	return nil
}

func validateReport(report Report) error {
	if !validReportDetail(report) {
		return fmt.Errorf("%w: invalid activity detail", ErrInvalidObservation)
	}
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
	if location.PanePID < 0 || (!location.Empty() && !location.Kind.Valid()) {
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
