package registry

import (
	"encoding/json"
	"fmt"
	"time"
)

type observationWire struct {
	Harness  Harness             `json:"harness"`
	At       time.Time           `json:"at"`
	Subject  ObservationIdentity `json:"subject"`
	Kind     string              `json:"kind"`
	Evidence json.RawMessage     `json:"evidence"`
}

func (o Observation) MarshalJSON() ([]byte, error) {
	evidence, err := json.Marshal(o.Evidence)
	if err != nil {
		return nil, fmt.Errorf("encoding evidence: %w", err)
	}
	data, err := json.Marshal(observationWire{Harness: o.Harness, At: o.At, Subject: o.Subject, Kind: o.Kind(), Evidence: evidence})
	if err != nil {
		return nil, fmt.Errorf("encoding observation: %w", err)
	}
	return data, nil
}

func (o *Observation) UnmarshalJSON(data []byte) error {
	var wire observationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decoding observation: %w", err)
	}
	var evidence Evidence
	switch wire.Kind {
	case "":
		evidence = nil
	case "report":
		evidence = new(Report)
	case "sighting":
		evidence = new(Sighting)
	case "placement":
		evidence = new(Placement)
	case "listing":
		evidence = new(Listing)
	case "reading":
		evidence = new(Reading)
	default:
		return fmt.Errorf("%w: unknown evidence kind %q", ErrInvalidObservation, wire.Kind)
	}
	if evidence != nil {
		if err := json.Unmarshal(wire.Evidence, evidence); err != nil {
			return fmt.Errorf("decoding evidence: %w", err)
		}
	}
	*o = Observation{Harness: wire.Harness, At: wire.At, Subject: wire.Subject, Evidence: evidence}
	return nil
}
