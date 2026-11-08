package registry

import (
	"encoding/json"
	"fmt"
	"time"
)

type sightingJSON Sighting

type processObservationWire struct {
	sightingJSON

	ObservedAt time.Time `json:"observed_at"`
}

type placementJSON Placement

type multiplexerObservationWire struct {
	placementJSON

	// Snapshots call the pane location context; input observations call it location.
	Location   *Location `json:"location,omitempty"`
	Context    Location  `json:"context"`
	ObservedAt time.Time `json:"observed_at"`
}

type observationWire struct {
	Harness  Harness             `json:"harness"`
	At       time.Time           `json:"at"`
	Subject  ObservationIdentity `json:"subject"`
	Kind     EvidenceKind        `json:"kind"`
	Evidence json.RawMessage     `json:"evidence"`
}

func (o Observation) MarshalJSON() ([]byte, error) {
	evidence, err := json.Marshal(o.Evidence)
	if err != nil {
		return nil, fmt.Errorf("encoding evidence: %w", err)
	}

	data, err := json.Marshal(
		observationWire{
			Harness:  o.Harness,
			At:       o.At,
			Subject:  o.Subject,
			Kind:     o.Kind(),
			Evidence: evidence,
		},
	)
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

	if wire.Kind != "" {
		var known bool
		if evidence, known = newEvidence(wire.Kind); !known {
			return fmt.Errorf("%w: unknown evidence kind %q", ErrInvalidObservation, wire.Kind)
		}
	}

	if evidence != nil {
		if err := json.Unmarshal(wire.Evidence, evidence); err != nil {
			return fmt.Errorf("decoding evidence: %w", err)
		}
	}

	*o = Observation{Harness: wire.Harness, At: wire.At, Subject: wire.Subject, Evidence: evidence}

	return nil
}

func (o ProcessObservation) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(
		processObservationWire{sightingJSON: sightingJSON(o.Sighting), ObservedAt: o.ObservedAt},
	)
	if err != nil {
		return nil, fmt.Errorf("encoding process observation: %w", err)
	}

	return data, nil
}

func (o *ProcessObservation) UnmarshalJSON(data []byte) error {
	var wire processObservationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decoding process observation: %w", err)
	}

	*o = ProcessObservation{Sighting: Sighting(wire.sightingJSON), ObservedAt: wire.ObservedAt}

	return nil
}

func (o MultiplexerObservation) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(
		multiplexerObservationWire{
			placementJSON: placementJSON(o.Placement),
			Location:      nil,
			Context:       o.Placement.Location,
			ObservedAt:    o.ObservedAt,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("encoding multiplexer observation: %w", err)
	}

	return data, nil
}

func (o *MultiplexerObservation) UnmarshalJSON(data []byte) error {
	var wire multiplexerObservationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decoding multiplexer observation: %w", err)
	}

	placement := Placement(wire.placementJSON)
	placement.Location = wire.Context
	*o = MultiplexerObservation{Placement: placement, ObservedAt: wire.ObservedAt}

	return nil
}
