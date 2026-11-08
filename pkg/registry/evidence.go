package registry

import (
	"encoding/json"
	"time"
)

const (
	EvidenceReport       EvidenceKind = "report"
	EvidenceSighting     EvidenceKind = "sighting"
	EvidencePlacement    EvidenceKind = "placement"
	EvidenceListing      EvidenceKind = "listing"
	EvidenceReading      EvidenceKind = "reading"
	EvidenceVerification EvidenceKind = "verification"
)

// EvidenceKind names an [Evidence] type on the wire and in journal entries.
type EvidenceKind string

//sumtype:decl
type Evidence interface {
	kind() EvidenceKind
	validate() error
	fields() evidenceFields
	slot(session Session) (any, time.Time)
	store(session *Session, subject ObservationIdentity, at time.Time)
}

//nolint:recvcheck // Value marshaling must also apply to observation values in containers.
type Observation struct {
	Harness  Harness             `json:"harness"`
	At       time.Time           `json:"at"`
	Subject  ObservationIdentity `json:"subject"`
	Evidence Evidence            `json:"-"`
}

type Report struct {
	NativeEvidence

	//exhaustruct:optional
	DetailObservedAt *time.Time       `json:"detail_observed_at,omitempty"`
	Detail           *ActivityDetail  `json:"detail,omitempty"`
	Claim            *Presence        `json:"claim,omitempty"`
	Process          *ProcessIdentity `json:"process,omitempty"`
	Location         *Location        `json:"location,omitempty"`
	Listing          *Listing         `json:"listing,omitempty"`
	Payload          json.RawMessage  `json:"payload,omitempty"`
}

type Sighting struct {
	Process ProcessIdentity `json:"process"`
	Present bool            `json:"present"`
}

// Verification reconciles an existing session when its owning process is unknown.
// Presence must be gone or unknown; a live process requires a Sighting.
type Verification struct {
	Presence Presence `json:"presence"`
	Reason   string   `json:"reason"`
}

type Placement struct {
	Process  ProcessIdentity `json:"process"`
	Location Location        `json:"location"`
}

type Reading ScreenObservation

//exhaustruct:optional
type evidenceFields struct {
	process  *ProcessIdentity
	present  *bool
	activity *Activity
	location *Location
	listing  *Listing
	reading  *ScreenObservation
}

type processEvidence interface {
	setProcess(process *ProcessIdentity)
}
type (
	activityEvidence interface{ setActivity(activity *Activity) }
	locationEvidence interface{ setLocation(location *Location) }
	listingEvidence  interface{ setListing(listing *Listing) }
)

type NativeEvidence struct {
	Reporter   Reporter          `json:"reporter"`
	Event      string            `json:"event,omitempty"`
	Lifecycle  *NativeLifecycle  `json:"lifecycle,omitempty"`
	Activity   *Activity         `json:"activity,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

func (*Report) kind() EvidenceKind       { return EvidenceReport }
func (*Sighting) kind() EvidenceKind     { return EvidenceSighting }
func (*Placement) kind() EvidenceKind    { return EvidencePlacement }
func (*Listing) kind() EvidenceKind      { return EvidenceListing }
func (*Reading) kind() EvidenceKind      { return EvidenceReading }
func (*Verification) kind() EvidenceKind { return EvidenceVerification }

func newEvidence(kind EvidenceKind) (Evidence, bool) {
	switch kind {
	case EvidenceReport:
		return new(Report), true
	case EvidenceSighting:
		return new(Sighting), true
	case EvidencePlacement:
		return new(Placement), true
	case EvidenceListing:
		return new(Listing), true
	case EvidenceReading:
		return new(Reading), true
	case EvidenceVerification:
		return new(Verification), true
	}

	return nil, false
}

func (o Observation) Kind() EvidenceKind {
	if o.Evidence == nil {
		return ""
	}

	return o.Evidence.kind()
}

// Report returns the native report, or nil when the evidence is another kind.
func (o Observation) Report() *Report {
	report, _ := o.Evidence.(*Report)

	return report
}

func (o Observation) ProcessIdentity() *ProcessIdentity { return o.fields().process }
func (o Observation) Present() *bool                    { return o.fields().present }
func (o Observation) ActivityClaim() *Activity          { return o.fields().activity }
func (o Observation) Location() *Location               { return o.fields().location }
func (o Observation) Listing() *Listing                 { return o.fields().listing }
func (o Observation) Reading() *ScreenObservation       { return o.fields().reading }

func (r *Report) fields() evidenceFields {
	return evidenceFields{
		process:  r.Process,
		activity: r.Activity,
		location: r.Location,
		listing:  r.Listing,
	}
}

func (s *Sighting) fields() evidenceFields {
	fields := evidenceFields{present: &s.Present}

	var empty ProcessIdentity
	if s.Process != empty {
		fields.process = &s.Process
	}

	return fields
}

func (p *Placement) fields() evidenceFields {
	return evidenceFields{process: &p.Process, location: &p.Location}
}
func (l *Listing) fields() evidenceFields { return evidenceFields{listing: l} }
func (r *Reading) fields() evidenceFields {
	return evidenceFields{
		process:  &r.Process,
		activity: &r.Activity,
		reading:  (*ScreenObservation)(r),
	}
}
func (*Verification) fields() evidenceFields { return evidenceFields{} }

func (o *Observation) SetProcess(process *ProcessIdentity) {
	if evidence, ok := o.Evidence.(processEvidence); ok {
		evidence.setProcess(process)
	}
}

func (o *Observation) SetActivity(activity *Activity) {
	if evidence, ok := o.Evidence.(activityEvidence); ok {
		evidence.setActivity(activity)
	}
}

func (o *Observation) SetLocation(location *Location) {
	if evidence, ok := o.Evidence.(locationEvidence); ok {
		evidence.setLocation(location)
	}
}

func (o *Observation) SetListing(listing *Listing) {
	if _, ok := o.Evidence.(*Listing); ok {
		o.Evidence = listing
		return
	}

	if evidence, ok := o.Evidence.(listingEvidence); ok {
		evidence.setListing(listing)
	}
}
func (r *Report) setProcess(process *ProcessIdentity) { r.Process = process }
func (s *Sighting) setProcess(process *ProcessIdentity) {
	var empty ProcessIdentity

	s.Process = empty
	if process != nil {
		s.Process = *process
	}
}

func (p *Placement) setProcess(process *ProcessIdentity) {
	if process != nil {
		p.Process = *process
	}
}

func (r *Reading) setProcess(process *ProcessIdentity) {
	if process != nil {
		r.Process = *process
	}
}
func (r *Report) setActivity(activity *Activity) { r.Activity = activity }
func (r *Reading) setActivity(activity *Activity) {
	if activity != nil {
		r.Activity = *activity
	}
}
func (r *Report) setLocation(location *Location) { r.Location = location }
func (p *Placement) setLocation(location *Location) {
	if location != nil {
		p.Location = *location
	}
}
func (r *Report) setListing(listing *Listing) { r.Listing = listing }

func (o Observation) fields() evidenceFields {
	if o.Evidence == nil {
		return evidenceFields{}
	}

	return o.Evidence.fields()
}
