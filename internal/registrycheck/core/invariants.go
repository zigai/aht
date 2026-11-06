package core

import (
	"fmt"
	"path/filepath"
	"time"
)

type Event struct {
	Kind        string
	Process     string
	Harness     string
	SessionID   string
	SessionPath string
	At          time.Time
	Ends        bool
	Present     bool
}

func Transition(before Session, after Session, event Event) error {
	rules := []func(Session, Session, Event) error{
		nativeIdentityEndInvariant,
		terminalTimeInvariant,
		retiredProcessInvariant,
		ownProcessTerminalInvariant,
		oldProcessEndInvariant,
		exitEvidenceInvariant,
		processEvidenceTimeInvariant,
		activityDecisionTimeInvariant,
	}
	for _, rule := range rules {
		if err := rule(before, after, event); err != nil {
			return err
		}
	}

	return nil
}

func eventTargetsSession(event Event, session Session) bool {
	target := event.Harness == session.Harness
	if event.SessionID != "" && session.SessionID != "" {
		return target && event.SessionID == session.SessionID
	}

	if event.SessionPath != "" && session.SessionPath != "" {
		return target && filepath.Clean(event.SessionPath) == filepath.Clean(session.SessionPath)
	}

	return target
}

func nativeIdentityEndInvariant(before Session, after Session, event Event) error {
	changed := after.Process != before.Process || after.Incarnation != before.Incarnation ||
		after.Presence != before.Presence || after.Activity != before.Activity ||
		after.NativeEnded != before.NativeEnded || !after.DecisionAt.Equal(before.DecisionAt)
	if event.Kind == "report" && event.Ends && !eventTargetsSession(event, before) && changed {
		return fmt.Errorf("%w: another native identity's end changed %q", ErrNativeEnd, before.ID)
	}

	return nil
}

func terminalIncarnation(state string) bool { return state == "ended" || state == "exited" }

func terminalTimeInvariant(before Session, after Session, _ Event) error {
	if terminalIncarnation(before.Incarnation) && terminalIncarnation(after.Incarnation) &&
		before.Process == after.Process && after.TransitionAt.Before(before.TransitionAt) {
		return fmt.Errorf("%w: terminal time regressed", ErrTime)
	}

	return nil
}

func retiredProcessInvariant(before Session, after Session, event Event) error {
	if before.PreviousProcess != "" && before.PreviousProcess == event.Process &&
		before.Process != event.Process && after.Process != before.Process {
		return fmt.Errorf("%w: retired process became current", ErrLiveness)
	}

	return nil
}

func ownProcessTerminalEvent(event Event, session Session) bool {
	return event.Kind == "sighting" && !event.Present && event.Process == session.Process ||
		event.Kind == "report" && event.Ends && eventTargetsSession(event, session) &&
			(event.Process == session.Process || event.Process == "")
}

func ownProcessTerminalInvariant(before Session, after Session, event Event) error {
	if !ownProcessTerminalEvent(event, before) {
		return nil
	}

	if event.At.Before(before.EvidenceAt) {
		if after != before {
			return fmt.Errorf(
				"%w: stale own-process terminal evidence changed %q",
				ErrLiveness,
				before.ID,
			)
		}

		return nil
	}

	if after.Presence != "gone" || after.Activity != "" {
		return fmt.Errorf(
			"%w: own-process terminal evidence did not end %q",
			ErrLiveness,
			before.ID,
		)
	}

	return nil
}

func oldProcessEndInvariant(before Session, after Session, event Event) error {
	if event.Kind == "report" && event.Ends && event.Process != "" && before.Process != "" &&
		event.Process != before.Process &&
		(after.Process != before.Process || after.Incarnation != before.Incarnation) {
		return fmt.Errorf("%w: old process end changed current incarnation", ErrNativeEnd)
	}

	return nil
}

func exitEvidenceInvariant(before Session, after Session, event Event) error {
	if event.Kind == "sighting" && !event.Present && after.Process == before.Process &&
		!after.EvidenceAt.Equal(before.EvidenceAt) {
		return fmt.Errorf("%w: exit scan became own-process evidence", ErrTime)
	}

	return nil
}

func processEvidenceTimeInvariant(before Session, after Session, event Event) error {
	if before.Process != after.Process && before.Process != "" &&
		event.At.Before(before.EvidenceAt) {
		return fmt.Errorf("%w: process changed before its last evidence", ErrTime)
	}

	return nil
}

func activityDecisionTimeInvariant(before Session, after Session, _ Event) error {
	if before.Process == after.Process && !terminalIncarnation(before.Incarnation) &&
		after.Presence != "gone" && !before.DecisionAt.IsZero() && after.DecisionAt.Before(before.DecisionAt) {
		return fmt.Errorf("%w: activity decision regressed within incarnation", ErrTime)
	}

	return nil
}
