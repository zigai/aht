package registry

import (
	"encoding/json"
	"time"
)

//sumtype:decl
type Evidence interface {
	evidence()
	validate() error
}

//nolint:recvcheck // Value marshaling must also apply to observation values in containers.
type Observation struct {
	Harness  Harness             `json:"harness"`
	At       time.Time           `json:"at"`
	Subject  ObservationIdentity `json:"subject"`
	Evidence Evidence            `json:"-"`
}

type Report struct {
	Reporter   Reporter          `json:"reporter"`
	Event      string            `json:"event,omitempty"`
	Lifecycle  *NativeLifecycle  `json:"lifecycle,omitempty"`
	Claim      *Presence         `json:"claim,omitempty"`
	Activity   *Activity         `json:"activity,omitempty"`
	Process    *ProcessIdentity  `json:"process,omitempty"`
	Location   *Location         `json:"location,omitempty"`
	Listing    *Listing          `json:"listing,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Payload    json.RawMessage   `json:"payload,omitempty"`
}

type Sighting struct {
	Process ProcessIdentity `json:"process"`
	Present bool            `json:"present"`
}

type Placement struct {
	Process  ProcessIdentity `json:"process"`
	Location Location        `json:"location"`
}

type Reading ScreenObservation

func (*Report) evidence()    {}
func (*Sighting) evidence()  {}
func (*Placement) evidence() {}
func (*Listing) evidence()   {}
func (*Reading) evidence()   {}

func (o Observation) Kind() string {
	switch o.Evidence.(type) {
	case *Report:
		return "report"
	case *Sighting:
		return "sighting"
	case *Placement:
		return "placement"
	case *Listing:
		return "listing"
	case *Reading:
		return "reading"
	case nil:
		return ""
	}
	return ""
}

func (o Observation) Report() *Report {
	report, ok := o.Evidence.(*Report)
	if !ok || report == nil {
		return new(Report)
	}
	return report
}

func (o Observation) ProcessIdentity() *ProcessIdentity {
	switch evidence := o.Evidence.(type) {
	case *Report:
		return evidence.Process
	case *Sighting:
		var empty ProcessIdentity
		if evidence.Process != empty {
			return &evidence.Process
		}
	case *Placement:
		return &evidence.Process
	case *Reading:
		return &evidence.Process
	case *Listing, nil:
		return nil
	}
	return nil
}

func (o Observation) Present() *bool {
	if evidence, ok := o.Evidence.(*Sighting); ok && evidence != nil {
		return &evidence.Present
	}
	return nil
}

func (o Observation) ActivityClaim() *Activity {
	switch evidence := o.Evidence.(type) {
	case *Report:
		return evidence.Activity
	case *Reading:
		return &evidence.Activity
	case *Sighting, *Placement, *Listing, nil:
		return nil
	}
	return nil
}

func (o Observation) Location() *Location {
	switch evidence := o.Evidence.(type) {
	case *Report:
		return evidence.Location
	case *Placement:
		return &evidence.Location
	case *Sighting, *Reading, *Listing, nil:
		return nil
	}
	return nil
}

func (o Observation) Listing() *Listing {
	switch evidence := o.Evidence.(type) {
	case *Report:
		return evidence.Listing
	case *Listing:
		return evidence
	case *Sighting, *Placement, *Reading, nil:
		return nil
	}
	return nil
}

func (o Observation) Reading() *ScreenObservation {
	if evidence, ok := o.Evidence.(*Reading); ok {
		return (*ScreenObservation)(evidence)
	}
	return nil
}

func (o *Observation) SetProcess(process *ProcessIdentity) {
	switch evidence := o.Evidence.(type) {
	case *Report:
		evidence.Process = process
	case *Sighting:
		if process != nil {
			evidence.Process = *process
		} else {
			var empty ProcessIdentity
			evidence.Process = empty
		}
	case *Placement:
		if process != nil {
			evidence.Process = *process
		}
	case *Reading:
		if process != nil {
			evidence.Process = *process
		}
	case *Listing, nil:
	}
}

func (o *Observation) SetPresent(present *bool) {
	if evidence, ok := o.Evidence.(*Sighting); ok && present != nil {
		evidence.Present = *present
	}
}

func (o *Observation) SetActivity(activity *Activity) {
	switch evidence := o.Evidence.(type) {
	case *Report:
		evidence.Activity = activity
	case *Reading:
		if activity != nil {
			evidence.Activity = *activity
		}
	case *Sighting, *Placement, *Listing, nil:
	}
}

func (o *Observation) SetLocation(location *Location) {
	switch evidence := o.Evidence.(type) {
	case *Report:
		evidence.Location = location
	case *Placement:
		if location != nil {
			evidence.Location = *location
		}
	case *Sighting, *Reading, *Listing, nil:
	}
}

func (o *Observation) SetListing(listing *Listing) {
	switch evidence := o.Evidence.(type) {
	case *Report:
		evidence.Listing = listing
	case *Listing:
		o.Evidence = listing
	case *Sighting, *Placement, *Reading, nil:
	}
}

func (o *Observation) SetReading(reading *ScreenObservation) { o.Evidence = (*Reading)(reading) }
