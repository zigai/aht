package registry

import (
	"reflect"
	"time"
)

const (
	ActivityDetailPermission ActivityDetail = "permission"
	ActivityDetailQuestion   ActivityDetail = "question"
	ActivityDetailGeneral    ActivityDetail = "general"
	ActivityDetailUsageLimit ActivityDetail = "usage_limit"
)

const (
	DetailQualityCurrent     DetailQuality = "current"
	DetailQualityStale       DetailQuality = "stale"
	DetailQualityMissing     DetailQuality = "missing"
	DetailQualityUnsupported DetailQuality = "unsupported"
)

type (
	ActivityDetail string
	DetailQuality  string
)

type DetailSupport struct {
	Permission bool `json:"permission"`
	Question   bool `json:"question"`
	UsageLimit bool `json:"usage_limit"`
}

type DetailCapabilities struct {
	Native DetailSupport `json:"native"`
	Screen DetailSupport `json:"screen"`
}

type DetailEvidence struct {
	Value      ActivityDetail `json:"value"`
	ObservedAt time.Time      `json:"observed_at"`
}

type StateDetail struct {
	Value      ActivityDetail `json:"value,omitempty"`
	Quality    DetailQuality  `json:"quality"`
	Authority  Authority      `json:"authority,omitempty"`
	ObservedAt time.Time      `json:"observed_at,omitzero"`
}

func (d ActivityDetail) ValidFor(activity Activity) bool {
	switch activity {
	case ActivityWaiting:
		return d == ActivityDetailPermission || d == ActivityDetailQuestion
	case ActivityFailed:
		return d == ActivityDetailGeneral || d == ActivityDetailUsageLimit
	case ActivityRunning, ActivityIdle, ActivityInterrupted, ActivityUnknown:
		return false
	}

	return false
}

func (support DetailSupport) Supports(activity Activity) bool {
	return activity == ActivityFailed ||
		activity == ActivityWaiting && (support.Permission || support.Question)
}

func hasDetailActivity(activity *Activity) bool {
	return activity != nil && (*activity == ActivityWaiting || *activity == ActivityFailed)
}

func (s *Session) resolveDetail(policy Policy, now time.Time) {
	s.Detail = nil

	activity := s.Activity()
	if s.Presence() != PresenceLive || !hasDetailActivity(activity) {
		return
	}

	decision := s.Decision()
	if decision == nil || s.Process == nil || !decision.Process.Equal(*s.Process) {
		s.Detail = &StateDetail{
			Value:      "",
			Quality:    DetailQualityMissing,
			Authority:  "",
			ObservedAt: time.Time{},
		}

		return
	}

	evidence, support := s.selectedDetail(policy, *activity, decision.Authority)

	detail := StateDetail{
		Value:      "",
		Quality:    DetailQualityMissing,
		Authority:  decision.Authority,
		ObservedAt: time.Time{},
	}
	if !support.Supports(*activity) {
		detail.Quality = DetailQualityUnsupported
	}

	if *activity == ActivityFailed {
		detail.Value = ActivityDetailGeneral
	}

	if evidence != nil && evidence.Value.ValidFor(*activity) {
		detail.Value, detail.ObservedAt, detail.Quality = evidence.Value, evidence.ObservedAt, DetailQualityCurrent
		// A terminal record is the program's standing state, so it does not age.
		if decision.Authority != AuthorityTerminal &&
			invalidHookTimeReason(evidence.ObservedAt, now) != "" {
			detail.Quality = DetailQualityStale
		}
	}

	s.Detail = &detail
}

func (s Session) selectedDetail(
	policy Policy,
	activity Activity,
	authority Authority,
) (*DetailEvidence, DetailSupport) {
	switch authority {
	case AuthorityHook:
		return s.matchingNativeDetail(policy), policy.Details.Native
	case AuthorityScreen, AuthorityTerminal:
		support := policy.Details.Screen
		if authority == AuthorityTerminal {
			support = DetailSupport{Permission: true, Question: true, UsageLimit: false}
		}

		screen := s.Observations.Screen
		if screen != nil && screen.Process.Equal(*s.Process) && screen.Activity == activity &&
			screen.Detail != "" {
			return &DetailEvidence{
				Value:      screen.Detail,
				ObservedAt: screen.ObservedAt,
			}, support
		}

		return nil, support
	case AuthorityProcess:
		return nil, DetailSupport{}
	}

	return nil, DetailSupport{}
}

func (s Session) matchingNativeDetail(policy Policy) *DetailEvidence {
	native := s.Observations.Native
	if native == nil || !native.Process.Equal(*s.Process) || nativeEnded(native) {
		return nil
	}

	if native.Reporter.Integration != policy.Reporter {
		return nil
	}

	return native.Detail
}

func (s Session) nativeDetail(
	report *Report,
	process ProcessIdentity,
	at time.Time,
) *DetailEvidence {
	if report.Ends() || report.starts() {
		return nil
	}

	if report.Detail != nil {
		if *report.Detail == "" {
			return nil
		}

		if report.DetailObservedAt != nil {
			at = *report.DetailObservedAt
		}

		return &DetailEvidence{Value: *report.Detail, ObservedAt: at}
	}

	if report.Activity != nil && *report.Activity == ActivityFailed {
		return &DetailEvidence{Value: ActivityDetailGeneral, ObservedAt: at}
	}

	if report.Activity != nil || !s.retainNativeDetail(report, process) {
		return nil
	}

	return clonePtr(s.Observations.Native.Detail)
}

func (s Session) retainNativeDetail(report *Report, process ProcessIdentity) bool {
	previous := s.Observations.Native

	return previous != nil && previous.Detail != nil && !nativeEnded(previous) &&
		process.Complete() &&
		previous.Process.Equal(process) &&
		previous.Reporter.Integration == report.Reporter.Integration &&
		s.Presence() != PresenceGone
}

func validDetail(detail *ActivityDetail, activity *Activity) bool {
	return detail == nil || *detail == "" || activity != nil && detail.ValidFor(*activity)
}

func validDetailEvidence(detail *DetailEvidence, activity *Activity) bool {
	return detail == nil ||
		activity != nil && detail.Value.ValidFor(*activity) && !detail.ObservedAt.IsZero()
}

func validStateDetail(detail *StateDetail, activity *Activity) bool {
	if detail == nil {
		return true
	}

	if !hasDetailActivity(activity) || !optionalAuthorityValid(detail.Authority) {
		return false
	}

	if detail.Value != "" && !detail.Value.ValidFor(*activity) {
		return false
	}

	switch detail.Quality {
	case DetailQualityCurrent, DetailQualityStale:
		return detail.Value != "" && !detail.ObservedAt.IsZero() && detail.Authority != ""
	case DetailQualityMissing, DetailQualityUnsupported:
		return detail.ObservedAt.IsZero() &&
			(detail.Value == "" || detail.Value == ActivityDetailGeneral)
	}

	return false
}

func (s *MemoryStore) refreshDetailsLocked() {
	var (
		candidate snapshot
		changes   []Change
	)

	now := s.now().UTC()
	for id, stored := range s.snapshot.Sessions {
		session := stored
		session.resolveDetail(s.reducer.rules.Policy(session.Harness), now)

		if reflect.DeepEqual(stored.Detail, session.Detail) {
			continue
		}

		if candidate.Sessions == nil {
			candidate = cloneRegistrySnapshotForMutation(s.snapshot)
		}

		candidate.Sessions[id] = session
		changes = append(changes, Change{ID: id, Removed: false})
	}

	if len(changes) > 0 {
		s.acceptUnjournaledLocked(candidate, changes)
	}
}

func validReportDetail(report Report) bool {
	if !validDetail(report.Detail, report.Activity) {
		return false
	}

	return report.DetailObservedAt == nil ||
		report.Detail != nil && *report.Detail != "" && !report.DetailObservedAt.IsZero()
}

func optionalAuthorityValid(
	authority Authority,
) bool {
	return authority == "" || authority.Valid()
}
