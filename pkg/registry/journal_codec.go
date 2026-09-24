package registry

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type journalWire struct {
	Sequence     uint64         `json:"sequence"`
	ReceivedAt   time.Time      `json:"received_at"`
	Observations []Observation  `json:"observations,omitempty"`
	Payloads     [][]byte       `json:"payloads,omitempty"`
	DeleteAfter  *time.Duration `json:"delete_after,omitempty"`
	Reset        bool           `json:"reset,omitempty"`
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
	data, err := json.Marshal(journalWire{Sequence: entry.Sequence, ReceivedAt: entry.ReceivedAt, Observations: observations, Payloads: payloads, DeleteAfter: entry.DeleteAfter, Reset: entry.Reset})
	if err != nil {
		return nil, fmt.Errorf("encoding journal command: %w", err)
	}
	return data, nil
}

func (entry *journalEntry) UnmarshalJSON(data []byte) error {
	var wire journalWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decoding journal command: %w", err)
	}
	for index := range wire.Observations {
		if index < len(wire.Payloads) {
			wire.Observations[index].Report().Payload = wire.Payloads[index]
		}
	}
	*entry = journalEntry{Sequence: wire.Sequence, ReceivedAt: wire.ReceivedAt, Observations: wire.Observations, DeleteAfter: wire.DeleteAfter, Reset: wire.Reset}
	return nil
}
