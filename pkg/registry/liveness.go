package registry

import "time"

//sumtype:decl
type Liveness interface{ liveness() }

type Unknown struct {
	Activity Activity
	Decision *ActivityDecision
}

type Live struct {
	Activity Activity
	Decision *ActivityDecision
}

type Gone struct {
	At       time.Time
	Reason   string
	Decision *ActivityDecision
}

func (Unknown) liveness() {}
func (Live) liveness()    {}
func (Gone) liveness()    {}

func NewLiveness(presence Presence, activity Activity, decision *ActivityDecision) Liveness {
	switch presence {
	case PresenceGone:
		return Gone{At: time.Time{}, Reason: "", Decision: decision}
	case PresenceLive:
		return Live{Activity: activity, Decision: decision}
	case PresenceUnknown:
		return Unknown{Activity: activity, Decision: decision}
	default:
		return Unknown{Activity: activity, Decision: decision}
	}
}

func ActivityValue(activity *Activity) Activity {
	if activity == nil {
		return ActivityUnknown
	}
	return *activity
}

func (s Session) Presence() Presence {
	switch s.Liveness.(type) {
	case Live:
		return PresenceLive
	case Gone:
		return PresenceGone
	case Unknown, nil:
		return PresenceUnknown
	}
	return PresenceUnknown
}

func (s Session) Activity() *Activity {
	switch state := s.Liveness.(type) {
	case Live:
		return new(state.Activity)
	case Unknown:
		return new(state.Activity)
	case Gone:
		return nil
	case nil:
		return new(ActivityUnknown)
	}
	return nil
}

func (s Session) Decision() *ActivityDecision {
	switch state := s.Liveness.(type) {
	case Live:
		return state.Decision
	case Unknown:
		return state.Decision
	case Gone:
		return state.Decision
	case nil:
		return nil
	}
	return nil
}

func (s *Session) setPresence(presence Presence) {
	s.Liveness = NewLiveness(presence, ActivityValue(s.Activity()), s.Decision())
}

func (s *Session) setActivity(activity *Activity) {
	if s.Presence() == PresenceGone {
		return
	}
	s.Liveness = NewLiveness(s.Presence(), ActivityValue(activity), s.Decision())
}

func (s *Session) setDecision(decision *ActivityDecision) {
	if gone, ok := s.Liveness.(Gone); ok {
		gone.Decision = decision
		s.Liveness = gone
		return
	}
	s.Liveness = NewLiveness(s.Presence(), ActivityValue(s.Activity()), decision)
}
