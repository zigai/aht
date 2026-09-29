package codex_test

import (
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestRunningClockSurvivesScreenGapsAndLongToolSilence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state := registry.State{}
	state, _ = applyClockObservation(t, state, clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning))

	for index, activity := range []registry.Activity{registry.ActivityRunning, registry.ActivityUnknown, registry.ActivityIdle, registry.ActivityWaiting, registry.ActivityRunning} {
		observationAt := at.Add(time.Duration(index+1) * time.Minute)
		var session registry.Session
		state, session = applyClockObservation(t, state, clockReading(observationAt, process, activity))
		requireClockState(t, session, registry.ActivityRunning, at)
	}

	_, session := applyClockObservation(t, state, clockReport(at.Add(6*time.Minute), process, "PostToolUse", registry.ActivityRunning))
	requireClockState(t, session, registry.ActivityRunning, at)
}

func TestNativeTurnTransitionsUpdateClock(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state := registry.State{}
	transitions := []struct {
		event    string
		activity registry.Activity
	}{
		{"UserPromptSubmit", registry.ActivityRunning},
		{"PermissionRequest", registry.ActivityWaiting},
		{"PostToolUse", registry.ActivityRunning},
		{"Stop", registry.ActivityIdle},
		{"UserPromptSubmit", registry.ActivityRunning},
		{"Interrupt", registry.ActivityInterrupted},
		{"UserPromptSubmit", registry.ActivityRunning},
	}

	for index, transition := range transitions {
		observationAt := at.Add(time.Duration(index) * time.Minute)
		var session registry.Session
		state, session = applyClockObservation(t, state, clockReport(observationAt, process, transition.event, transition.activity))
		requireClockState(t, session, transition.activity, observationAt)
		state, session = applyClockObservation(t, state, clockReading(observationAt.Add(45*time.Second), process, registry.ActivityUnknown))
		requireClockState(t, session, transition.activity, observationAt)
	}
}

func TestAutomaticCompactionKeepsRunningClock(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state, _ := applyClockObservation(t, registry.State{}, clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning))
	state, _ = applyClockObservation(t, state, clockReport(at.Add(time.Minute), process, "PreCompact", registry.ActivityRunning))
	observation := clockReport(at.Add(2*time.Minute), process, "SessionStart", registry.ActivityRunning)
	observation.Report().Activity = nil
	observation.Report().Lifecycle = new(registry.NativeLifecycleStart)
	state, session := applyClockObservation(t, state, observation)
	requireClockState(t, session, registry.ActivityRunning, at)
	_, session = applyClockObservation(t, state, clockReading(at.Add(3*time.Minute), process, registry.ActivityUnknown))
	requireClockState(t, session, registry.ActivityRunning, at)
}

func TestProcessReplacementReturnsToScreenFallback(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state, _ := applyClockObservation(t, registry.State{}, clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning))
	process.StartIdentity = "boot:124"
	state, _ = applyClockObservation(t, state, registry.Observation{
		Harness: registry.Harness("codex"), At: at.Add(time.Minute),
		Subject:  registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Sighting{Process: process, Present: true},
	})
	_, session := applyClockObservation(t, state, clockReading(at.Add(2*time.Minute), process, registry.ActivityIdle))
	requireClockState(t, session, registry.ActivityIdle, at.Add(2*time.Minute))
	if session.Decision() == nil || session.Decision().Authority != registry.AuthorityScreen {
		t.Fatalf("decision = %v, want screen fallback", session.Decision())
	}
}

func TestScreenFallbackWithoutMatchingNativeEvidence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	for _, reporter := range []string{"", "unrelated-hook"} {
		t.Run(reporter, func(t *testing.T) {
			t.Parallel()

			state := registry.State{}
			if reporter != "" {
				observation := clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning)
				observation.Report().Reporter.Integration = reporter
				state, _ = applyClockObservation(t, state, observation)
			}
			_, session := applyClockObservation(t, state, clockReading(at.Add(time.Minute), process, registry.ActivityWaiting))
			requireClockState(t, session, registry.ActivityWaiting, at.Add(time.Minute))
			if session.Decision() == nil || session.Decision().Authority != registry.AuthorityScreen {
				t.Fatalf("decision = %v, want screen fallback", session.Decision())
			}
		})
	}
}

func clockReport(at time.Time, process registry.ProcessIdentity, event string, activity registry.Activity) registry.Observation {
	return registry.Observation{
		Harness: registry.Harness("codex"), At: at,
		Subject: registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Report{
			Reporter: registry.Reporter{Integration: "codex-hook"},
			Event:    event, Claim: new(registry.PresenceLive), Activity: new(activity), Process: &process,
		},
	}
}

func clockReading(at time.Time, process registry.ProcessIdentity, activity registry.Activity) registry.Observation {
	return registry.Observation{
		Harness: registry.Harness("codex"), At: at,
		Subject: registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Reading{
			Activity: activity, Authority: registry.AuthorityScreen, Reason: "manifest_rule", RuleID: "screen-probe", Process: process,
		},
	}
}

func applyClockObservation(t *testing.T, state registry.State, observation registry.Observation) (registry.State, registry.Session) {
	t.Helper()

	state, _, err := registry.NewReducer(catalog.Rules{}).Apply(state, []registry.Observation{observation}, observation.At)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(state.Sessions))
	}
	for _, session := range state.Sessions {
		return state, session
	}
	return state, registry.Session{}
}

func requireClockState(t *testing.T, session registry.Session, activity registry.Activity, since time.Time) {
	t.Helper()

	if session.Activity() == nil || *session.Activity() != activity {
		t.Fatalf("activity = %v, want %s", session.Activity(), activity)
	}
	if !session.ActivityChangedAt.Equal(since) {
		t.Fatalf("activity clock = %s, want %s", session.ActivityChangedAt, since)
	}
}
