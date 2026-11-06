package registry

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type journalWire[T any] struct {
	Sequence     uint64         `json:"sequence"`
	ReceivedAt   time.Time      `json:"received_at"`
	Observations []T            `json:"observations,omitempty"`
	Payloads     [][]byte       `json:"payloads,omitempty"`
	DeleteAfter  *time.Duration `json:"delete_after,omitempty"`
	Reset        bool           `json:"reset,omitempty"`
}

type decodedJournalEntry struct {
	journalEntry

	rejected map[int]error
}

func (entry journalEntry) MarshalJSON() ([]byte, error) {
	observations := slices.Clone(entry.Observations)

	payloads := make([][]byte, len(observations))
	for index := range observations {
		if report, ok := observations[index].Evidence.(*Report); ok && report != nil {
			cloned := *report
			payloads[index] = cloned.Payload
			cloned.Payload = nil
			observations[index].Evidence = &cloned
		}
	}

	data, err := json.Marshal(
		journalWire[Observation]{
			Sequence:     entry.Sequence,
			ReceivedAt:   entry.ReceivedAt,
			Observations: observations,
			Payloads:     payloads,
			DeleteAfter:  entry.DeleteAfter,
			Reset:        entry.Reset,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("encoding journal command: %w", err)
	}

	return data, nil
}

func decodeJournalEntry(data []byte) (decodedJournalEntry, error) {
	var wire journalWire[json.RawMessage]
	if err := json.Unmarshal(data, &wire); err != nil {
		return decodedJournalEntry{}, fmt.Errorf("decoding journal command: %w", err)
	}

	decoded := decodedJournalEntry{
		Sequence:     wire.Sequence,
		ReceivedAt:   wire.ReceivedAt,
		Observations: make([]Observation, len(wire.Observations)),
		DeleteAfter:  wire.DeleteAfter,
		Reset:        wire.Reset,
		rejected:     nil,
	}
	for index, raw := range wire.Observations {
		observation := &decoded.Observations[index]
		if err := json.Unmarshal(raw, observation); err != nil {
			if decoded.rejected == nil {
				decoded.rejected = make(map[int]error)
			}

			decoded.rejected[index] = fmt.Errorf("%w: %w", ErrInvalidObservation, err)

			continue
		}

		if report, ok := observation.Evidence.(*Report); ok && report != nil &&
			index < len(wire.Payloads) {
			report.Payload = wire.Payloads[index]
		}
	}

	return decoded, nil
}

func (entry *journalEntry) UnmarshalJSON(data []byte) error {
	decoded, err := decodeJournalEntry(data)
	if err != nil {
		return err
	}

	for index := range decoded.Observations {
		if err := decoded.rejected[index]; err != nil {
			return err
		}
	}

	*entry = decoded.journalEntry

	return nil
}
