package registry

import (
	"reflect"
	"slices"
	"time"
)

const (
	DetailPermission ActivityDetail = "permission"
	DetailQuestion   ActivityDetail = "question"
	DetailGeneral    ActivityDetail = "general"
	DetailUsageLimit ActivityDetail = "usage_limit"
)

const (
	DetailCurrent     DetailQuality = "current"
	DetailStale       DetailQuality = "stale"
	DetailMissing     DetailQuality = "missing"
	DetailUnsupported DetailQuality = "unsupported"
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
		return d == DetailPermission || d == DetailQuestion
	case ActivityFailed:
		return d == DetailGeneral || d == DetailUsageLimit
	case ActivityRunning, ActivityIdle, ActivityInterrupted, ActivityUnknown:
		return false
	}
	return false
}

func (support DetailSupport) Supports(activity Activity) bool {
	return activity == ActivityFailed || activity == ActivityWaiting && (support.Permission || support.Question)
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
		s.Detail = &StateDetail{Value: "", Quality: DetailMissing, Authority: "", ObservedAt: time.Time{}}
		return
	}
	evidence, support := s.selectedDetail(policy, *activity, decision.Authority)
	detail := StateDetail{Value: "", Quality: DetailMissing, Authority: decision.Authority, ObservedAt: time.Time{}}
	if !support.Supports(*activity) {
		detail.Quality = DetailUnsupported
	}
	if *activity == ActivityFailed {
		detail.Value = DetailGeneral
	}
	if evidence != nil && evidence.Value.ValidFor(*activity) {
		detail.Value, detail.ObservedAt, detail.Quality = evidence.Value, evidence.ObservedAt, DetailCurrent
		if invalidHookTimeReason(evidence.ObservedAt, now) != "" {
			detail.Quality = DetailStale
		}
	}
	s.Detail = &detail
}

func (s Session) selectedDetail(policy Policy, activity Activity, authority Authority) (*DetailEvidence, DetailSupport) {
	switch authority {
	case AuthorityHook:
		return s.matchingNativeDetail(policy), policy.Details.Native
	case AuthorityScreen:
		screen := s.Observations.Screen
		if screen != nil && screen.Process.Equal(*s.Process) && screen.Activity == activity && screen.Detail != "" {
			return &DetailEvidence{Value: screen.Detail, ObservedAt: screen.ObservedAt}, policy.Details.Screen
		}
		return nil, policy.Details.Screen
	case AuthorityProcess:
		return nil, DetailSupport{Permission: false, Question: false, UsageLimit: false}
	}
	return nil, DetailSupport{Permission: false, Question: false, UsageLimit: false}
}

func (s Session) matchingNativeDetail(policy Policy) *DetailEvidence {
	native := s.Observations.Native
	if native == nil || !native.Process.Equal(*s.Process) || nativeEnded(native) {
		return nil
	}
	if native.Reporter.Integration != policy.Reporter && !slices.Contains(policy.DetailReporters, native.Reporter.Integration) {
		return nil
	}
	return native.Detail
}

func (s Session) nativeDetail(report *Report, process ProcessIdentity, at time.Time) *DetailEvidence {
	if reportEnds(report) || startsNativeLifecycle(report.Lifecycle) {
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
		return &DetailEvidence{Value: DetailGeneral, ObservedAt: at}
	}
	if report.Activity != nil || !s.retainNativeDetail(report, process) {
		return nil
	}
	return clonePtr(s.Observations.Native.Detail)
}

func startsNativeLifecycle(lifecycle *NativeLifecycle) bool {
	return lifecycle != nil && (*lifecycle == NativeLifecycleStart || *lifecycle == NativeLifecycleResume)
}

func (s Session) retainNativeDetail(report *Report, process ProcessIdentity) bool {
	previous := s.Observations.Native
	return previous != nil && previous.Detail != nil && !nativeEnded(previous) && process.Complete() && previous.Process.Equal(process) && previous.Reporter.Integration == report.Reporter.Integration && s.Presence() != PresenceGone
}

func validDetail(detail *ActivityDetail, activity *Activity) bool {
	return detail == nil || *detail == "" || activity != nil && detail.ValidFor(*activity)
}

func validDetailEvidence(detail *DetailEvidence, activity *Activity) bool {
	return detail == nil || activity != nil && detail.Value.ValidFor(*activity) && !detail.ObservedAt.IsZero()
}

func validStateDetail(detail *StateDetail, activity *Activity) bool {
	if detail == nil {
		return true
	}
	if !hasDetailActivity(activity) {
		return false
	}
	if detail.Value != "" && !detail.Value.ValidFor(*activity) {
		return false
	}
	if !validDetailAuthority(detail.Authority) {
		return false
	}
	switch detail.Quality {
	case DetailCurrent, DetailStale:
		return detail.Value != "" && !detail.ObservedAt.IsZero() && detail.Authority != ""
	case DetailMissing, DetailUnsupported:
		return detail.ObservedAt.IsZero() && (detail.Value == "" || detail.Value == DetailGeneral)
	}
	return false
}

func validDetailAuthority(authority Authority) bool {
	switch authority {
	case "", AuthorityHook, AuthorityScreen, AuthorityProcess:
		return true
	}
	return false
}

func (s *MemoryStore) refreshDetailsLocked() {
	var candidate snapshot
	var changes []Change
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
		s.acceptSnapshotLocked(candidate, changes)
	}
}

func validReportDetail(report Report) bool {
	if !validDetail(report.Detail, report.Activity) {
		return false
	}
	return report.DetailObservedAt == nil || report.Detail != nil && *report.Detail != "" && !report.DetailObservedAt.IsZero()
}
