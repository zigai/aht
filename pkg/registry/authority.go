package registry

import (
	"slices"
	"time"
)

const hookReportStale = "integration_report_stale"

type HookEvaluation struct {
	Active         bool
	Fresh          bool
	ProcessMatches bool
	Reason         string
}

func EvaluateHook(session Session, policy Policy, now time.Time) HookEvaluation {
	native, failure, ok := matchingHookObservation(session, policy)
	if !ok {
		return failure
	}

	if reason := invalidHookTimeReason(native.ObservedAt, now); reason != "" {
		return HookEvaluation{Active: false, Fresh: false, ProcessMatches: true, Reason: reason}
	}

	return HookEvaluation{
		Active:         true,
		Fresh:          true,
		ProcessMatches: true,
		Reason:         "matching_live_process_report",
	}
}

func ActivityAuthority(session Session, policy Policy, now time.Time) (Authority, string) {
	if reading := terminalReading(session); reading != nil &&
		!hookHoldsTurn(session, policy, *reading, now) {
		return AuthorityTerminal, reading.Reason
	}

	if policy.Authority == AuthorityScreen {
		return AuthorityScreen, "screen_primary"
	}

	evaluation := EvaluateHook(session, policy, now)
	if policy.ScreenFallback && !evaluation.Active {
		if policy.RetainNativeActivity && evaluation.Reason == hookReportStale {
			return AuthorityHook, evaluation.Reason
		}

		return AuthorityScreen, evaluation.Reason
	}

	return AuthorityHook, evaluation.Reason
}

func (o ScreenObservation) authority() Authority {
	if o.Authority == AuthorityTerminal {
		return AuthorityTerminal
	}

	return AuthorityScreen
}

func terminalReading(session Session) *ScreenObservation {
	reading := session.Observations.Screen
	if reading == nil || reading.Authority != AuthorityTerminal ||
		reading.Activity == ActivityUnknown || session.Presence() == PresenceGone ||
		session.Process == nil || !reading.Process.Equal(*session.Process) {
		return nil
	}

	return reading
}

func hookHoldsTurn(
	session Session,
	policy Policy,
	reading ScreenObservation,
	now time.Time,
) bool {
	if reading.Activity != ActivityIdle {
		return false
	}

	native, _, ok := matchingHookObservation(session, policy)
	if !ok || *native.Activity != ActivityRunning ||
		!slices.Contains(policy.HeldTurnEvents, native.Event) {
		return false
	}

	reason := invalidHookTimeReason(native.ObservedAt, now)

	return reason == "" || policy.RetainNativeActivity && reason == hookReportStale
}

func matchingHookObservation(
	session Session,
	policy Policy,
) (*NativeObservation, HookEvaluation, bool) {
	if policy.Authority != AuthorityHook || policy.Reporter == "" {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "hook_not_activity_authority",
		}, false
	}

	native := session.Observations.Native
	if native == nil {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "integration_report_missing",
		}, false
	}

	if native.Reporter.Integration != policy.Reporter {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "integration_identity_mismatch",
		}, false
	}

	if native.Activity == nil || *native.Activity == ActivityUnknown {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "integration_activity_missing",
		}, false
	}

	if nativeEnded(native) || session.Presence() == PresenceGone {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "integration_ended",
		}, false
	}

	if session.Process == nil {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "agent_process_missing",
		}, false
	}

	if !native.Process.Equal(*session.Process) {
		return nil, HookEvaluation{
			Active:         false,
			Fresh:          false,
			ProcessMatches: false,
			Reason:         "agent_process_replaced",
		}, false
	}

	return native, HookEvaluation{}, true
}

func invalidHookTimeReason(observedAt time.Time, now time.Time) string {
	if observedAt.After(now) {
		return "integration_observation_from_future"
	}

	if now.Sub(observedAt) > IntegrationActivityLease {
		return hookReportStale
	}

	return ""
}

func nativeEnded(native *NativeObservation) bool {
	if native.Presence != nil && *native.Presence == PresenceGone {
		return true
	}

	return native.Lifecycle != nil && *native.Lifecycle == NativeLifecycleEnd
}
