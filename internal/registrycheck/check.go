package registrycheck

import (
	"fmt"
	"time"

	"github.com/zigai/aht/v2/internal/registrycheck/core"
	"github.com/zigai/aht/v2/pkg/registry"
)

type Coverage = core.Coverage

// Sessions checks properties preserved by the reducer and snapshot validation.
func Sessions(sessions []registry.Session) error {
	projected := project(sessions)

	if err := core.Sessions(projected); err != nil {
		return fmt.Errorf("registry sessions: %w", err)
	}

	return nil
}

func NewCoverage() *Coverage { return core.NewCoverage() }

func Verification(before registry.Session, after registry.Session) error {
	if err := core.Verification(
		string(before.Presence()),
		string(after.Presence()),
		before.Process != nil && before.Process.Complete(),
	); err != nil {
		return fmt.Errorf("registry verification: %w", err)
	}

	return nil
}

func Transition(
	before registry.Session,
	after registry.Session,
	observation registry.Observation,
) error {
	projected := project([]registry.Session{before, after})

	process := ""
	if value := observation.ProcessIdentity(); value != nil {
		process = core.ProcessKey(value.PID, value.StartIdentity)
	}

	err := core.Transition(projected[0], projected[1], core.Event{
		Process: process, Harness: string(observation.Harness),
		SessionID: observation.Subject.SessionID, SessionPath: observation.Subject.SessionPath,
		At: observation.At, NativeEnd: observation.Report().Ends(),
		Absent: observation.Present() != nil && !*observation.Present(),
	})
	if err != nil {
		return fmt.Errorf("checking registry transition: %w", err)
	}

	return nil
}

func project(sessions []registry.Session) []core.Session {
	projected := make([]core.Session, len(sessions))
	for index, session := range sessions {
		var activity string
		if value := session.Activity(); value != nil {
			activity = string(*value)
		}

		var process string
		if session.Process != nil {
			process = core.ProcessKey(session.Process.PID, session.Process.StartIdentity)
		}

		var decisionAt time.Time
		if decision := session.Decision(); decision != nil {
			decisionAt = decision.ObservedAt
		}

		var previousProcess string
		if session.Incarnation.Previous != nil {
			previousProcess = core.ProcessKey(
				session.Incarnation.Previous.PID,
				session.Incarnation.Previous.StartIdentity,
			)
		}

		projected[index] = core.Session{
			TransitionAt:    session.Incarnation.TransitionAt,
			PreviousProcess: previousProcess,
			SessionID:       session.SessionID,
			SessionPath:     session.SessionPath,
			Process:         process,
			IncarnationProcess: core.ProcessKey(
				session.Incarnation.Process.PID,
				session.Incarnation.Process.StartIdentity,
			),
			Incarnation: string(
				session.Incarnation.State,
			),
			EvidenceAt:  session.Incarnation.EvidenceAt,
			DecisionAt:  decisionAt,
			ID:          session.ID,
			Harness:     string(session.Harness),
			CreatedAt:   session.CreatedAt,
			UpdatedAt:   session.UpdatedAt,
			HasLiveness: session.Liveness != nil,
			Presence:    string(session.Presence()),
			Activity:    activity,
			ValidState: session.Presence().Valid() &&
				(session.Activity() == nil || session.Activity().Valid()),
			NativeEnded: session.Observations.Native != nil &&
				session.Observations.Native.Lifecycle != nil &&
				*session.Observations.Native.Lifecycle == registry.NativeLifecycleEnd,
		}
	}

	return projected
}
