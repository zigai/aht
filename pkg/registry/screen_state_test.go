package registry

import (
	"context"
	"errors"
	"testing"
	"time"
)

//nolint:cyclop // assertions cover independent hook and screen authority invariants
func TestScreenStateIsAuthoritativeUnderScreenPolicy(t *testing.T) {
	t.Parallel()
	for _, harness := range []Harness{HarnessCodex, HarnessClaude} {
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()
			store := NewJournal(t.TempDir()+"/state.json", fixtureRules{authority: AuthorityScreen})
			at := time.Now().UTC()
			process := ProcessIdentity{PID: 100, StartIdentity: "boot:100", Executable: string(harness)}
			running := ActivityRunning
			presence := PresenceLive
			session, err := store.Observe(context.Background(), Observation{Harness: harness, At: at, Subject: ObservationIdentity{SessionID: "session"}, Evidence: &Report{Event: "prompt_submit", Claim: &presence, Activity: &running, Process: &process}})
			if err != nil {
				t.Fatal(err)
			}
			if session.Activity() == nil || *session.Activity() != ActivityUnknown {
				t.Fatalf("incomplete hook authored %s activity: %#v", harness, session.Activity())
			}
			if session.Observations.Native == nil || session.Observations.Native.Activity == nil || *session.Observations.Native.Activity != running {
				t.Fatalf("hook authority metadata was not preserved: %#v", session.Observations.Native)
			}
			idle := ActivityIdle
			screen := &ScreenObservation{Activity: idle, Authority: "screen", Reason: "manifest_rule", RuleID: "input_prompt", ManifestSource: "bundled", ManifestVersion: 1, Process: process, ObservedAt: at.Add(time.Second)}
			session, err = store.Observe(context.Background(), Observation{Harness: harness, At: at.Add(time.Second), Subject: ObservationIdentity{}, Evidence: (*Reading)(screen)})
			if err != nil {
				t.Fatal(err)
			}
			if session.Activity() == nil || *session.Activity() != ActivityIdle || session.Decision() == nil || session.Decision().Authority != "screen" {
				t.Fatalf("screen did not author activity: %#v", session)
			}
		})
	}
}

func TestStaleIntegrationDoesNotSuppressScreenFallback(t *testing.T) {
	t.Parallel()
	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Now().UTC()
	process := ProcessIdentity{PID: 150, StartIdentity: "boot:150", Executable: "pi"}
	presence := PresenceLive
	idle := ActivityIdle
	_, err := store.Observe(context.Background(), Observation{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: "pi-stale"}, Evidence: &Report{Reporter: Reporter{Integration: "pi-extension"}, Event: "agent_settled", Claim: &presence, Activity: &idle, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	waiting := ActivityWaiting
	screenAt := at.Add(IntegrationActivityLease + time.Second)
	screen := &ScreenObservation{Activity: waiting, Authority: "screen", Reason: "manifest_rule", RuleID: "permission_prompt", ManifestSource: "bundled", ManifestVersion: 1, FallbackForIntegration: "pi-extension", FallbackReason: "integration_report_stale", Process: process, ObservedAt: screenAt}
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessPi, At: screenAt, Subject: ObservationIdentity{SessionID: "pi-stale"}, Evidence: (*Reading)(screen)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityWaiting || session.Decision() == nil || session.Decision().Authority != "screen" || session.Decision().FallbackReason != "integration_report_stale" {
		t.Fatalf("stale integration suppressed fallback: %#v", session)
	}
}

func TestDelayedNativeActivityCannotOverwriteNewerScreenDecision(t *testing.T) {
	t.Parallel()

	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 151, StartIdentity: "boot:151", Executable: "pi"}
	presence := PresenceLive
	running := ActivityRunning
	_, err := store.Observe(context.Background(), Observation{Harness: HarnessPi, At: at, Subject: ObservationIdentity{SessionID: "pi-delayed"}, Evidence: &Report{Reporter: Reporter{Integration: "pi-extension"}, Event: "agent_start", Claim: &presence, Activity: &running, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}

	screenAt := at.Add(IntegrationActivityLease + time.Second)
	idle := ActivityIdle
	screen := &ScreenObservation{
		Activity: idle, Authority: "screen", Reason: "manifest_rule", RuleID: "input_prompt",
		ManifestSource: "bundled", ManifestVersion: 1, FallbackForIntegration: "pi-extension",
		FallbackReason: "integration_report_stale", Process: process, ObservedAt: screenAt,
	}
	_, err = store.Observe(context.Background(), Observation{Harness: HarnessPi, At: screenAt, Subject: ObservationIdentity{SessionID: "pi-delayed"}, Evidence: (*Reading)(screen)})
	if err != nil {
		t.Fatal(err)
	}

	waiting := ActivityWaiting
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessPi, At: at.Add(10 * time.Second), Subject: ObservationIdentity{SessionID: "pi-delayed"}, Evidence: &Report{Reporter: Reporter{Integration: "pi-extension"}, Event: "permission_prompt", Claim: &presence, Activity: &waiting, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityIdle || !session.ActivityChangedAt.Equal(screenAt) || session.Decision() == nil || session.Decision().Authority != "screen" {
		t.Fatalf("delayed native activity overwrote newer screen decision: %#v", session)
	}
}

//nolint:cyclop // regression verifies replacement and stale cross-source race invariants
func TestProcessReplacementClearsScreenActivity(t *testing.T) {
	t.Parallel()
	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Now().UTC()
	oldProcess := ProcessIdentity{PID: 102, StartIdentity: "boot:old", Executable: "codex"}
	idle := ActivityIdle
	screen := &ScreenObservation{Activity: idle, Authority: "screen", Reason: "manifest_rule", RuleID: "input_prompt", Process: oldProcess, ObservedAt: at}
	_, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at, Subject: ObservationIdentity{SessionID: "same"}, Evidence: (*Reading)(screen)})
	if err != nil {
		t.Fatal(err)
	}
	newProcess := ProcessIdentity{PID: 103, StartIdentity: "boot:new", Executable: "codex"}
	presence := PresenceLive
	session, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at.Add(time.Second), Subject: ObservationIdentity{SessionID: "same"}, Evidence: &Report{Event: "session_start", Claim: &presence, Process: &newProcess}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Activity() == nil || *session.Activity() != ActivityUnknown || session.Observations.Screen != nil || session.Decision() == nil || session.Decision().Reason != "process_replaced" {
		t.Fatalf("replacement retained old screen activity: %#v", session)
	}
	staleIdle := ActivityIdle
	staleScreen := &ScreenObservation{Activity: staleIdle, Authority: "screen", Reason: "manifest_rule", RuleID: "input_prompt", ManifestSource: "bundled", ManifestVersion: 1, FallbackForIntegration: "", Process: oldProcess, ObservedAt: at.Add(500 * time.Millisecond)}
	if _, err := store.Observe(context.Background(), Observation{Harness: HarnessCodex, At: at.Add(500 * time.Millisecond), Subject: ObservationIdentity{SessionID: "same"}, Evidence: (*Reading)(staleScreen)}); !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("stale old-process screen error = %v, want conflict", err)
	}
	session, err = store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Process == nil || !session.Process.Equal(newProcess) || session.Activity() == nil || *session.Activity() != ActivityUnknown {
		t.Fatalf("stale screen restored replaced process: %#v", session)
	}
}
