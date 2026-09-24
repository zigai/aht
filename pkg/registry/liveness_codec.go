package registry

import (
	"encoding/json"
	"fmt"
	"time"
)

type sessionJSON Session

type livenessJSON struct {
	Kind     Presence          `json:"kind"`
	Activity *Activity         `json:"activity,omitempty"`
	Decision *ActivityDecision `json:"decision,omitempty"`
	At       time.Time         `json:"at,omitzero"`
	Reason   string            `json:"reason,omitempty"`
}

type sessionWire struct {
	sessionJSON

	Liveness livenessJSON `json:"liveness"`
}

func (s Session) MarshalJSON() ([]byte, error) {
	life := livenessJSON{Kind: s.Presence(), Activity: s.Activity(), Decision: s.Decision(), At: time.Time{}, Reason: ""}
	if gone, ok := s.Liveness.(Gone); ok {
		life.At = gone.At
		life.Reason = gone.Reason
	}
	data, err := json.Marshal(sessionWire{sessionJSON: sessionJSON(s), Liveness: life})
	if err != nil {
		return nil, fmt.Errorf("encoding session: %w", err)
	}
	return data, nil
}

func (s *Session) UnmarshalJSON(data []byte) error {
	var wire sessionWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decoding session: %w", err)
	}
	if !wire.Liveness.Kind.IsValid() {
		return fmt.Errorf("%w: invalid liveness %q", ErrCorruptStore, wire.Liveness.Kind)
	}
	if (wire.Liveness.Kind == PresenceGone) != (wire.Liveness.Activity == nil) {
		return fmt.Errorf("%w: liveness activity mismatch", ErrCorruptStore)
	}
	*s = Session(wire.sessionJSON)
	s.Liveness = NewLiveness(wire.Liveness.Kind, ActivityValue(wire.Liveness.Activity), wire.Liveness.Decision)
	if wire.Liveness.Kind == PresenceGone {
		s.Liveness = Gone{At: wire.Liveness.At, Reason: wire.Liveness.Reason, Decision: wire.Liveness.Decision}
	}
	return nil
}
