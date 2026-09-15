package agentstate

import (
	"time"

	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

const (
	AuthorityHook   Authority = "hook"
	AuthorityScreen Authority = "screen"
)

type Authority string

type Policy struct {
	Primary          Authority
	ScreenFallback   bool
	IntegrationValue string
}

type HookEvaluation struct {
	Active         bool
	Fresh          bool
	ProcessMatches bool
	Reason         string
}

func (a Authority) IsValid() bool {
	switch a {
	case AuthorityHook, AuthorityScreen:
		return true
	}
	return false
}

func PolicyFor(harness registry.Harness) Policy {
	auth, fallback, source := harnesscatalog.PolicyFor(harness)
	return Policy{
		Primary:          Authority(auth),
		ScreenFallback:   fallback,
		IntegrationValue: source,
	}
}

func SupportsScreen(harness registry.Harness) bool {
	return harnesscatalog.SupportsScreen(harness)
}

func EvaluateHook(session registry.Session, now time.Time) HookEvaluation {
	policy := PolicyFor(session.Harness)
	native, failure, ok := matchingHookObservation(session, policy)
	if !ok {
		return failure
	}
	if reason := invalidHookTimeReason(native.ObservedAt, now); reason != "" {
		return HookEvaluation{Active: false, Fresh: false, ProcessMatches: true, Reason: reason}
	}
	return HookEvaluation{Active: true, Fresh: true, ProcessMatches: true, Reason: "matching_live_process_report"}
}

func HookIsActive(session registry.Session, now time.Time) bool {
	return EvaluateHook(session, now).Active
}

func ShouldDetectScreen(session registry.Session, now time.Time) bool {
	policy := PolicyFor(session.Harness)
	if policy.Primary == AuthorityScreen {
		return true
	}
	return policy.ScreenFallback && !HookIsActive(session, now)
}

func matchingHookObservation(session registry.Session, policy Policy) (*registry.NativeObservation, HookEvaluation, bool) {
	if policy.Primary != AuthorityHook || policy.IntegrationValue == "" {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "hook_not_activity_authority"}, false
	}
	native := session.Observations.Native
	if native == nil {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "integration_report_missing"}, false
	}
	if native.Attributes["aht_integration"] != policy.IntegrationValue {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "integration_identity_mismatch"}, false
	}
	if native.Activity == nil || *native.Activity == registry.ActivityUnknown {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "integration_activity_missing"}, false
	}
	if nativeEnded(native) || session.Presence == registry.PresenceGone {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "integration_ended"}, false
	}
	if session.Process == nil {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "agent_process_missing"}, false
	}
	if !native.Process.Equal(*session.Process) {
		return nil, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: "agent_process_replaced"}, false
	}
	return native, HookEvaluation{Active: false, Fresh: false, ProcessMatches: false, Reason: ""}, true
}

func invalidHookTimeReason(observedAt time.Time, now time.Time) string {
	if observedAt.After(now) {
		return "integration_observation_from_future"
	}
	if now.Sub(observedAt) > registry.IntegrationActivityLease {
		return "integration_report_stale"
	}
	return ""
}

func nativeEnded(native *registry.NativeObservation) bool {
	if native.Presence != nil && *native.Presence == registry.PresenceGone {
		return true
	}
	return native.Lifecycle != nil && *native.Lifecycle == registry.NativeLifecycleEnd
}
