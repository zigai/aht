package registry

import "time"

const sessionReplacedReason = "session_replaced"

const (
	IncarnationStarting IncarnationState = "starting"
	IncarnationLive     IncarnationState = "live"
	IncarnationEnded    IncarnationState = "ended"
	IncarnationExited   IncarnationState = "exited"
)

const (
	incarnationContinue incarnationAction = iota
	incarnationReject
	incarnationIgnore
	incarnationVerify
	incarnationRetire
	incarnationReplace
	incarnationBind
	incarnationResume
)

type IncarnationState string

type Incarnation struct {
	State        IncarnationState `json:"state"`
	Process      ProcessIdentity  `json:"process,omitzero"`
	EvidenceAt   time.Time        `json:"evidence_at"`
	TransitionAt time.Time        `json:"transition_at"`
	Previous     *ProcessIdentity `json:"previous,omitempty"`
}

type incarnationTransition struct {
	next     Incarnation
	reset    bool
	terminal bool
	accepted bool
	ignored  bool
}

type lifecycleMachine struct{ session *Session }

type incarnationInput struct {
	current      Incarnation
	process      *ProcessIdentity
	report       *Report
	verification *Verification
	decision     *ActivityDecision
	kind         EvidenceKind
	at           time.Time
	retire       bool
	absent       bool
	end          bool
	start        bool
	terminal     bool
	different    bool
}

type incarnationAction uint8

type incarnationRule struct {
	matches func(incarnationInput) bool
	action  incarnationAction
}

type lifecycleEvidence interface {
	apply(machine lifecycleMachine, policy Policy, at time.Time)
}

func transitionIncarnation(
	session Session, observation Observation, at time.Time, retire bool,
) incarnationTransition {
	input := normalizeIncarnationInput(session, observation, at, retire)

	rules := []incarnationRule{
		{matches: incarnationInput.verificationRejected, action: incarnationReject},
		{matches: incarnationInput.verifies, action: incarnationVerify},
		{matches: incarnationInput.retirementIgnored, action: incarnationIgnore},
		{matches: incarnationInput.retirementRejected, action: incarnationReject},
		{matches: incarnationInput.retires, action: incarnationRetire},
		{matches: incarnationInput.replacementRejected, action: incarnationReject},
		{matches: incarnationInput.replaces, action: incarnationReplace},
		{matches: incarnationInput.terminalStale, action: incarnationReject},
		{matches: incarnationInput.bindsTerminalProcess, action: incarnationBind},
		{matches: incarnationInput.resumesTerminal, action: incarnationResume},
		{matches: incarnationInput.terminalRejected, action: incarnationReject},
	}
	for _, rule := range rules {
		if rule.matches(input) {
			return input.transition(rule.action)
		}
	}

	return input.transition(incarnationContinue)
}

func normalizeIncarnationInput(
	session Session, observation Observation, at time.Time, retire bool,
) incarnationInput {
	process := observation.ProcessIdentity()
	report, _ := observation.Evidence.(*Report)
	verification, _ := observation.Evidence.(*Verification)

	return incarnationInput{
		current:      session.Incarnation,
		process:      process,
		report:       report,
		verification: verification,
		decision:     session.Decision(),
		kind:         observation.Kind(),
		at:           at,
		retire:       retire,
		absent:       observation.Kind() == EvidenceSighting && !*observation.Present(),
		end:          report.Ends(),
		start:        report.starts(),
		terminal: session.Incarnation.State == IncarnationEnded ||
			session.Incarnation.State == IncarnationExited,
		different: process != nil && process.Complete() && session.Incarnation.Process.Complete() &&
			!session.Incarnation.Process.Equal(*process),
	}
}

func (input incarnationInput) verifies() bool { return input.verification != nil }

func (input incarnationInput) verificationRejected() bool {
	return input.verifies() && (input.terminal || input.current.Process.Complete() ||
		input.at.Before(input.current.EvidenceAt) ||
		input.decision != nil && input.at.Before(input.decision.ObservedAt))
}

func (input incarnationInput) retirementIgnored() bool { return input.retire && input.end }

func (input incarnationInput) retirementRejected() bool {
	return input.retire && input.at.Before(input.current.EvidenceAt)
}

func (input incarnationInput) retires() bool { return input.retire }

func (input incarnationInput) replacementRejected() bool {
	return input.different &&
		(input.previousProcess() || input.end || input.kind == EvidenceReading ||
			input.kind == EvidencePlacement || input.absent || input.at.Before(input.current.EvidenceAt))
}

func (input incarnationInput) previousProcess() bool {
	return input.current.Previous != nil && input.process != nil &&
		input.current.Previous.Equal(*input.process)
}

func (input incarnationInput) replaces() bool { return input.different }

func (input incarnationInput) terminalStale() bool {
	return (input.end || input.absent) && input.at.Before(input.current.EvidenceAt)
}

func (input incarnationInput) bindsTerminalProcess() bool {
	return input.terminal && !input.current.Process.Complete() && input.process != nil &&
		input.process.Complete() && input.report != nil && !input.end && !input.at.Before(input.current.EvidenceAt)
}

func (input incarnationInput) resumesTerminal() bool {
	return input.terminal && input.start &&
		input.at.After(maxTime(input.current.EvidenceAt, input.current.TransitionAt))
}

func (input incarnationInput) terminalRejected() bool {
	return input.terminal && !input.end && (input.report != nil || !input.absent)
}

func (input incarnationInput) transition(action incarnationAction) incarnationTransition {
	result := incarnationTransition{
		next: input.current, reset: false, terminal: false, accepted: true, ignored: false,
	}

	switch action {
	case incarnationReject, incarnationIgnore:
		result.accepted = false
		result.ignored = action == incarnationIgnore

		return result
	case incarnationVerify:
		result.next.State = IncarnationStarting
		if input.verification.Presence == PresenceGone {
			result.next.State = IncarnationExited
			result.terminal = true
		}

		result.next.TransitionAt = input.at

		return result
	case incarnationRetire:
		result.next.State = IncarnationExited
		result.next.TransitionAt = maxTime(input.current.TransitionAt, input.at)
		result.terminal = true

		return result
	case incarnationReplace:
		result.reset = true
		result.next = Incarnation{
			State: IncarnationStarting, Process: *input.process, EvidenceAt: time.Time{},
			TransitionAt: time.Time{}, Previous: new(input.current.Process),
		}
	case incarnationBind:
		result.reset = true
		result.next = Incarnation{
			State: IncarnationLive, Process: *input.process, EvidenceAt: time.Time{},
			TransitionAt: time.Time{}, Previous: nil,
		}
	case incarnationResume:
		result.reset = true
		result.next = Incarnation{
			State: IncarnationStarting, Process: input.current.Process, EvidenceAt: time.Time{},
			TransitionAt: time.Time{}, Previous: input.current.Previous,
		}
	case incarnationContinue:
		if input.current.State == "" {
			result.next.State = IncarnationStarting
		}
	}

	return input.applyEvidence(result)
}

func (input incarnationInput) applyEvidence(result incarnationTransition) incarnationTransition {
	if input.process != nil && input.process.Complete() {
		result.next.Process = *input.process
	}

	if result.next.TransitionAt.IsZero() {
		result.next.TransitionAt = input.at
	}

	switch {
	case input.end:
		result.next.State = IncarnationEnded
		result.next.TransitionAt = maxTime(input.current.TransitionAt, input.at)
		result.terminal = true
	case input.absent:
		result.next.State = IncarnationExited
		result.next.TransitionAt = maxTime(input.current.TransitionAt, input.at)
		result.terminal = true
	case input.kind == EvidenceSighting:
		result.next.State = IncarnationLive
		result.next.TransitionAt = maxTime(result.next.TransitionAt, input.at)
	case input.claimApplies(result.next):
		result.next.State = IncarnationStarting
		if *input.report.Claim == PresenceLive {
			result.next.State = IncarnationLive
		}

		result.next.TransitionAt = input.at
	case input.replacementReport(result):
		result.next.State = IncarnationLive
	}
	// Exit scans are not evidence that the old process was still running.
	if input.ownProcessEvidence(result.next) {
		result.next.EvidenceAt = maxTime(result.next.EvidenceAt, input.at)
	}

	return result
}

func (input incarnationInput) claimApplies(next Incarnation) bool {
	return input.report != nil && input.report.Claim != nil && !input.at.Before(next.TransitionAt)
}

func (input incarnationInput) ownProcessEvidence(next Incarnation) bool {
	return input.report != nil && !next.Process.Complete() ||
		input.process != nil && input.process.Equal(next.Process) && !input.absent
}

func (r *Report) starts() bool {
	return r != nil && r.Lifecycle != nil &&
		(*r.Lifecycle == NativeLifecycleStart || *r.Lifecycle == NativeLifecycleResume)
}

// Ends reports whether the report ends its session, by lifecycle or by a gone claim.
func (r *Report) Ends() bool {
	return r != nil && (r.Claim != nil && *r.Claim == PresenceGone ||
		r.Lifecycle != nil && *r.Lifecycle == NativeLifecycleEnd)
}

func (m lifecycleMachine) apply(observation Observation, policy Policy, at time.Time) {
	if evidence, ok := observation.Evidence.(lifecycleEvidence); ok {
		evidence.apply(m, policy, at)
	}
}

func (r *Report) apply(m lifecycleMachine, policy Policy, at time.Time) {
	if m.session.Observations.Native.Activity == nil || policy.Authority == AuthorityScreen ||
		!m.acceptsActivity(at) {
		return
	}

	if authority, _ := ActivityAuthority(*m.session, policy, at); authority == AuthorityTerminal {
		return
	}

	m.hookActivity(r.Event, at)
}

func (m lifecycleMachine) hookActivity(event string, at time.Time) {
	m.session.setActivity(clonePtr(m.session.Observations.Native.Activity))
	m.session.setDecision(&ActivityDecision{
		RuleID:          "",
		ManifestSource:  "",
		ManifestVersion: 0,
		FallbackReason:  "",
		Authority:       AuthorityHook,
		Reason:          event,
		Process:         m.session.Observations.Native.Process,
		ObservedAt:      at,
	})
}
func (*Reading) apply(m lifecycleMachine, policy Policy, at time.Time) { m.reading(policy, at) }
func (verification *Verification) apply(m lifecycleMachine, _ Policy, at time.Time) {
	m.verify(*verification, at)
}

func (m lifecycleMachine) commit(transition incarnationTransition, at time.Time) {
	if !transition.accepted {
		return
	}

	if transition.reset {
		m.session.Observations.Native = nil
		m.session.Observations.Screen = nil
		m.session.Observations.Process = nil
		m.session.Observations.Location = nil

		m.session.Liveness = Unknown{Activity: ActivityUnknown, Decision: &ActivityDecision{
			RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "",
			Authority:  AuthorityProcess,
			Reason:     "process_replaced",
			Process:    transition.next.Process,
			ObservedAt: at,
		}}
		if !transition.next.Process.Complete() {
			m.session.setDecision(nil)
		}
	}

	m.session.Incarnation = transition.next
	if transition.next.Process.Complete() {
		m.session.Process = new(transition.next.Process)
	}

	if transition.terminal {
		if !m.replacedTombstone() {
			m.session.Liveness = Gone{At: at, Reason: "process_gone", Decision: &ActivityDecision{
				RuleID: "", ManifestSource: "", ManifestVersion: 0, FallbackReason: "",
				Authority:  AuthorityProcess,
				Reason:     "process_gone",
				Process:    transition.next.Process,
				ObservedAt: at,
			}}
		}

		return
	}

	switch transition.next.State {
	case IncarnationLive:
		m.session.setPresence(PresenceLive)
	case IncarnationStarting:
		m.session.setPresence(PresenceUnknown)
	case IncarnationEnded, IncarnationExited:
	}
}

func (m lifecycleMachine) verify(verification Verification, at time.Time) {
	decision := &ActivityDecision{
		RuleID:          "",
		ManifestSource:  "",
		ManifestVersion: 0,
		FallbackReason:  "",
		Process: ProcessIdentity{
			PID: 0, PPID: 0, ProcessGroupID: 0, Foreground: false,
			StartIdentity: "", Executable: "", CWD: "", TTY: "",
		},
		Authority:  AuthorityProcess,
		Reason:     verification.Reason,
		ObservedAt: at,
	}
	if verification.Presence == PresenceGone {
		m.session.Liveness = Gone{At: at, Reason: verification.Reason, Decision: decision}
	} else {
		m.session.Liveness = Unknown{Activity: ActivityUnknown, Decision: decision}
	}
}

func (m lifecycleMachine) reading(policy Policy, at time.Time) {
	screen := m.session.Observations.Screen
	if screen == nil || m.session.Process == nil || !screen.Process.Equal(*m.session.Process) ||
		!m.acceptsActivity(at) {
		return
	}

	authority, _ := ActivityAuthority(*m.session, policy, at)
	if screen.Authority == AuthorityTerminal && authority == AuthorityHook {
		// The record was cleared or a held turn outranks it, so the hook's
		// last report is the state again.
		if native, _, ok := matchingHookObservation(*m.session, policy); ok {
			m.hookActivity(native.Event, at)
		}

		return
	}

	if authority != screen.authority() {
		return
	}

	m.session.setActivity(new(screen.Activity))
	m.session.setDecision(&ActivityDecision{
		Authority: screen.Authority, Reason: screen.Reason, RuleID: screen.RuleID,
		ManifestSource: screen.ManifestSource, ManifestVersion: screen.ManifestVersion,
		FallbackReason: screen.FallbackReason, Process: screen.Process, ObservedAt: at,
	})
}

func (m lifecycleMachine) acceptsActivity(at time.Time) bool {
	return m.session.Presence() != PresenceGone &&
		(m.session.Decision() == nil || !at.Before(m.session.Decision().ObservedAt))
}

func (m lifecycleMachine) replacedTombstone() bool {
	gone, ok := m.session.Liveness.(Gone)
	return ok && gone.Reason == sessionReplacedReason
}

func (input incarnationInput) replacementReport(result incarnationTransition) bool {
	return result.reset && input.different && input.report != nil
}

func (m lifecycleMachine) trackChanges(at time.Time) func() {
	previousPresence, previousActivity := m.session.Presence(), m.session.Activity()

	return func() {
		if m.session.Presence() != previousPresence {
			m.session.PresenceChangedAt = at
		}

		if !activityEqual(m.session.Activity(), previousActivity) {
			m.session.ActivityChangedAt = at
		}
	}
}
