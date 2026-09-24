package registry

import (
	"path/filepath"
	"time"
)

type Incarnation struct {
	ObservedAt    time.Time       `json:"observed_at"`
	NativeProcess ProcessIdentity `json:"native_process,omitzero"`
	NativeEnded   bool            `json:"native_ended"`
}

type lifecycleMachine struct {
	session *Session
}

func (m lifecycleMachine) apply(observation Observation, policy Policy, at time.Time) {
	switch observation.Evidence.(type) {
	case *Report:
		m.report(policy, at)
	case *Sighting:
		m.sighting(at)
	case *Reading:
		m.reading(policy, at)
	case *Placement, *Listing, nil:
	}
}

func (m lifecycleMachine) report(policy Policy, at time.Time) {
	native := m.session.Observations.Native
	if native == nil {
		return
	}
	if native.Lifecycle != nil {
		switch *native.Lifecycle {
		case NativeLifecycleEnd:
			m.end(at)
		case NativeLifecycleStart, NativeLifecycleResume:
			m.start(at)
		}
	}
	if native.Presence != nil {
		m.claim(*native.Presence, at)
	}
	if native.Activity != nil && policy.Authority != AuthorityScreen && m.acceptsActivity(at) {
		m.session.setActivity(clonePtr(native.Activity))
		m.session.setDecision(&ActivityDecision{Authority: AuthorityHook, Reason: native.Event, RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: native.Process, ObservedAt: at})
	}
}

func (m lifecycleMachine) start(at time.Time) {
	if m.session.Presence() == PresenceGone && at.After(m.session.PresenceChangedAt) {
		m.session.Liveness = Unknown{Activity: ActivityUnknown, Decision: m.session.Decision()}
	}
}

func (m lifecycleMachine) resume() {
	m.session.Liveness = Live{Activity: ActivityUnknown, Decision: m.session.Decision()}
	m.session.Incarnation.NativeEnded = false
}

func (m lifecycleMachine) claim(presence Presence, at time.Time) {
	switch presence {
	case PresenceGone:
		m.end(at)
	case PresenceLive, PresenceUnknown:
		if !m.session.Incarnation.NativeEnded && !at.Before(m.session.PresenceChangedAt) {
			m.session.setPresence(presence)
		}
	}
}

func (m lifecycleMachine) sighting(at time.Time) {
	process := m.session.Observations.Process
	if process == nil {
		return
	}
	if process.Present {
		m.claim(PresenceLive, at)
		return
	}
	if at.Before(m.session.Incarnation.ObservedAt) {
		return
	}
	m.end(at)
}

func (m lifecycleMachine) reading(policy Policy, at time.Time) {
	screen := m.session.Observations.Screen
	if screen == nil || m.session.Process == nil || !screen.Process.Equal(*m.session.Process) || !m.acceptsActivity(at) {
		return
	}
	if authority, _ := ActivityAuthority(*m.session, policy, at); authority != AuthorityScreen {
		return
	}
	m.session.setActivity(new(screen.Activity))
	m.session.setDecision(&ActivityDecision{Authority: screen.Authority, Reason: screen.Reason, RuleID: screen.RuleID, ManifestSource: screen.ManifestSource, ManifestVersion: screen.ManifestVersion, FallbackReason: screen.FallbackReason, Process: screen.Process, ObservedAt: at})
}

func (m lifecycleMachine) acceptsActivity(at time.Time) bool {
	return m.session.Presence() != PresenceGone && (m.session.Decision() == nil || !at.Before(m.session.Decision().ObservedAt))
}

func (m lifecycleMachine) processReplaced(process ProcessIdentity, at time.Time) {
	m.session.setActivity(new(ActivityUnknown))
	m.session.setDecision(&ActivityDecision{Authority: AuthorityProcess, Reason: "process_replaced", RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: process, ObservedAt: at})
	m.session.Observations.Screen = nil
	m.session.Incarnation.ObservedAt = at
}

func (m lifecycleMachine) end(at time.Time) {
	if at.Before(m.session.PresenceChangedAt) {
		return
	}
	hadActivity := m.session.Activity() != nil
	if m.session.Presence() != PresenceGone {
		m.session.Liveness = Gone{At: at, Reason: "process_gone", Decision: m.session.Decision()}
		m.session.PresenceChangedAt = at
	}
	if hadActivity {
		m.session.ActivityChangedAt = at
	}
	var process ProcessIdentity
	if m.session.Process != nil {
		process = *m.session.Process
	}
	m.session.setDecision(&ActivityDecision{Authority: AuthorityProcess, Reason: "process_gone", RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "", Process: process, ObservedAt: at})
}

func acceptsReport(session Session, observation Observation, at time.Time) bool {
	if observation.Kind() != "report" || session.Presence() != PresenceGone {
		return true
	}
	if beginsIncarnation(session, observation, at) {
		return true
	}
	lifecycle := observation.Report().Lifecycle
	return lifecycle != nil && (*lifecycle == NativeLifecycleStart || *lifecycle == NativeLifecycleResume) && at.After(session.PresenceChangedAt)
}

func beginsIncarnation(session Session, observation Observation, at time.Time) bool {
	if observation.Kind() != "report" || session.Presence() != PresenceGone || !canResumeIdentity(session, observation) {
		return false
	}
	if !at.After(session.PresenceChangedAt) || at.Before(session.Incarnation.ObservedAt) || reportEnds(observation.Report()) {
		return false
	}
	previous := session.Incarnation.NativeProcess
	if !previous.Complete() && session.Process != nil {
		previous = *session.Process
	}
	return previous.Complete() && !previous.Equal(*observation.ProcessIdentity())
}

func canResumeIdentity(session Session, observation Observation) bool {
	process := observation.ProcessIdentity()
	if process == nil || !process.Complete() || observationIdentityConflicts(session, observation.Subject) {
		return false
	}
	if observation.Subject.SessionID != "" && observation.Subject.SessionID == session.SessionID {
		return true
	}
	return observation.Subject.SessionPath != "" && session.SessionPath != "" && filepath.Clean(observation.Subject.SessionPath) == filepath.Clean(session.SessionPath)
}

func reportEnds(report *Report) bool {
	return (report.Claim != nil && *report.Claim == PresenceGone) || (report.Lifecycle != nil && *report.Lifecycle == NativeLifecycleEnd)
}

func incarnationFromEvidence(session Session) Incarnation {
	var incarnation Incarnation
	if native := session.Observations.Native; native != nil {
		incarnation.NativeProcess = native.Process
		incarnation.NativeEnded = native.Lifecycle != nil && *native.Lifecycle == NativeLifecycleEnd
	}
	if session.Process == nil {
		return incarnation
	}
	collect := func(process ProcessIdentity, at time.Time) {
		if process.Equal(*session.Process) {
			incarnation.ObservedAt = maxTime(incarnation.ObservedAt, at)
		}
	}
	if native := session.Observations.Native; native != nil {
		collect(native.Process, native.ObservedAt)
	}
	if sighting := session.Observations.Process; sighting != nil {
		collect(sighting.Process, sighting.ObservedAt)
	}
	if placement := session.Observations.Location; placement != nil {
		collect(placement.Process, placement.ObservedAt)
	}
	if reading := session.Observations.Screen; reading != nil {
		collect(reading.Process, reading.ObservedAt)
	}
	return incarnation
}
