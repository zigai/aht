package registry

import (
	"context"
	"testing"
	"time"
)

func TestScreenStateIsAuthoritativeUnderScreenPolicy(t *testing.T) {
	t.Parallel()

	for _, harness := range []Harness{harnessPrimary, harnessCatalog} {
		t.Run(string(harness), func(t *testing.T) {
			t.Parallel()
			store := NewJournal(t.TempDir()+"/state.json", fixtureRules{authority: AuthorityScreen})
			at := time.Now().UTC()
			process := ProcessIdentity{
				PID:           100,
				StartIdentity: "boot:100",
				Executable:    string(harness),
			}
			running := ActivityRunning
			presence := PresenceLive

			session, err := store.Observe(
				context.Background(),
				Observation{
					Harness: harness,
					At:      at,
					Subject: ObservationIdentity{SessionID: "session"},
					Evidence: &Report{
						Event:    "prompt_submit",
						Claim:    &presence,
						Activity: &running,
						Process:  &process,
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if session.Activity() == nil || *session.Activity() != ActivityUnknown {
				t.Fatalf("incomplete hook authored %s activity: %#v", harness, session.Activity())
			}

			if session.Observations.Native == nil || session.Observations.Native.Activity == nil ||
				*session.Observations.Native.Activity != running {
				t.Fatalf(
					"hook authority metadata was not preserved: %#v",
					session.Observations.Native,
				)
			}

			idle := ActivityIdle
			screen := &ScreenObservation{
				Activity:        idle,
				Authority:       "screen",
				Reason:          "manifest_rule",
				RuleID:          "input_prompt",
				ManifestSource:  "bundled",
				ManifestVersion: 1,
				Process:         process,
				ObservedAt:      at.Add(time.Second),
			}

			session, err = store.Observe(
				context.Background(),
				Observation{
					Harness:  harness,
					At:       at.Add(time.Second),
					Subject:  ObservationIdentity{},
					Evidence: (*Reading)(screen),
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if session.Activity() == nil || *session.Activity() != ActivityIdle ||
				session.Decision() == nil ||
				session.Decision().Authority != "screen" {
				t.Fatalf("screen did not author activity: %#v", session)
			}
		})
	}
}

func TestStaleIntegrationDoesNotSuppressScreenFallback(t *testing.T) {
	t.Parallel()
	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Now().UTC().Add(-time.Minute)
	process := ProcessIdentity{PID: 150, StartIdentity: "boot:150", Executable: "secondary"}
	presence := PresenceLive
	idle := ActivityIdle

	_, err := store.Observe(
		context.Background(),
		Observation{
			Harness: harnessSecondary,
			At:      at,
			Subject: ObservationIdentity{SessionID: "secondary-stale"},
			Evidence: &Report{
				Reporter: Reporter{Integration: "secondary-extension"},
				Event:    "agent_settled",
				Claim:    &presence,
				Activity: &idle,
				Process:  &process,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	waiting := ActivityWaiting
	screenAt := at.Add(IntegrationActivityLease + time.Second)
	screen := &ScreenObservation{
		Activity:               waiting,
		Authority:              "screen",
		Reason:                 "manifest_rule",
		RuleID:                 "permission_prompt",
		ManifestSource:         "bundled",
		ManifestVersion:        1,
		FallbackForIntegration: "secondary-extension",
		FallbackReason:         "integration_report_stale",
		Process:                process,
		ObservedAt:             screenAt,
	}

	session, err := store.Observe(
		context.Background(),
		Observation{
			Harness:  harnessSecondary,
			At:       screenAt,
			Subject:  ObservationIdentity{SessionID: "secondary-stale"},
			Evidence: (*Reading)(screen),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if session.Activity() == nil || *session.Activity() != ActivityWaiting ||
		session.Decision() == nil ||
		session.Decision().Authority != "screen" ||
		session.Decision().FallbackReason != "integration_report_stale" {
		t.Fatalf("stale integration suppressed fallback: %#v", session)
	}
}

func TestDelayedNativeActivityCannotOverwriteNewerScreenDecision(t *testing.T) {
	t.Parallel()

	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
	process := ProcessIdentity{PID: 151, StartIdentity: "boot:151", Executable: "secondary"}
	presence := PresenceLive
	running := ActivityRunning

	_, err := store.Observe(
		context.Background(),
		Observation{
			Harness: harnessSecondary,
			At:      at,
			Subject: ObservationIdentity{SessionID: "secondary-delayed"},
			Evidence: &Report{
				Reporter: Reporter{Integration: "secondary-extension"},
				Event:    "agent_start",
				Claim:    &presence,
				Activity: &running,
				Process:  &process,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	screenAt := at.Add(IntegrationActivityLease + time.Second)
	idle := ActivityIdle
	screen := &ScreenObservation{
		Activity:               idle,
		Authority:              "screen",
		Reason:                 "manifest_rule",
		RuleID:                 "input_prompt",
		ManifestSource:         "bundled",
		ManifestVersion:        1,
		FallbackForIntegration: "secondary-extension",
		FallbackReason:         "integration_report_stale",
		Process:                process,
		ObservedAt:             screenAt,
	}

	_, err = store.Observe(
		context.Background(),
		Observation{
			Harness:  harnessSecondary,
			At:       screenAt,
			Subject:  ObservationIdentity{SessionID: "secondary-delayed"},
			Evidence: (*Reading)(screen),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	waiting := ActivityWaiting

	session, err := store.Observe(
		context.Background(),
		Observation{
			Harness: harnessSecondary,
			At:      at.Add(10 * time.Second),
			Subject: ObservationIdentity{SessionID: "secondary-delayed"},
			Evidence: &Report{
				Reporter: Reporter{Integration: "secondary-extension"},
				Event:    "permission_prompt",
				Claim:    &presence,
				Activity: &waiting,
				Process:  &process,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if session.Activity() == nil || *session.Activity() != ActivityIdle ||
		!session.ActivityChangedAt.Equal(screenAt) ||
		session.Decision() == nil ||
		session.Decision().Authority != "screen" {
		t.Fatalf("delayed native activity overwrote newer screen decision: %#v", session)
	}
}

func TestProcessReplacementClearsScreenActivity(t *testing.T) {
	t.Parallel()
	store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
	at := time.Now().UTC()
	oldProcess := ProcessIdentity{PID: 102, StartIdentity: "boot:old", Executable: "primary"}
	idle := ActivityIdle
	screen := &ScreenObservation{
		Activity:   idle,
		Authority:  "screen",
		Reason:     "manifest_rule",
		RuleID:     "input_prompt",
		Process:    oldProcess,
		ObservedAt: at,
	}

	_, err := store.Observe(
		context.Background(),
		Observation{
			Harness:  harnessPrimary,
			At:       at,
			Subject:  ObservationIdentity{SessionID: "same"},
			Evidence: (*Reading)(screen),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	newProcess := ProcessIdentity{PID: 103, StartIdentity: "boot:new", Executable: "primary"}
	presence := PresenceLive

	session, err := store.Observe(
		context.Background(),
		Observation{
			Harness:  harnessPrimary,
			At:       at.Add(time.Second),
			Subject:  ObservationIdentity{SessionID: "same"},
			Evidence: &Report{Event: "session_start", Claim: &presence, Process: &newProcess},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if session.Activity() == nil || *session.Activity() != ActivityUnknown ||
		session.Observations.Screen != nil ||
		session.Decision() == nil ||
		session.Decision().Reason != "process_replaced" {
		t.Fatalf("replacement retained old screen activity: %#v", session)
	}

	staleIdle := ActivityIdle

	staleScreen := &ScreenObservation{
		Activity:               staleIdle,
		Authority:              "screen",
		Reason:                 "manifest_rule",
		RuleID:                 "input_prompt",
		ManifestSource:         "bundled",
		ManifestVersion:        1,
		FallbackForIntegration: "",
		Process:                oldProcess,
		ObservedAt:             at.Add(500 * time.Millisecond),
	}
	if _, err := store.Observe(
		context.Background(),
		Observation{
			Harness:  harnessPrimary,
			At:       at.Add(500 * time.Millisecond),
			Subject:  ObservationIdentity{SessionID: "same"},
			Evidence: (*Reading)(staleScreen),
		},
	); err != nil {
		t.Fatalf("stale old-process screen error = %v, want drop", err)
	}

	session, err = store.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}

	if session.Process == nil || !session.Process.Equal(newProcess) || session.Activity() == nil ||
		*session.Activity() != ActivityUnknown {
		t.Fatalf("stale screen restored replaced process: %#v", session)
	}
}

func TestTerminalRecordAndHookReportOrder(t *testing.T) {
	t.Parallel()

	type step struct {
		event    string
		terminal bool
		activity Activity
		detail   ActivityDetail
		after    time.Duration
	}

	hook := func(event string, activity Activity) step {
		return step{
			event:    event,
			terminal: false,
			activity: activity,
			detail:   "",
			after:    time.Second,
		}
	}
	terminal := func(activity Activity) step {
		return step{event: "", terminal: true, activity: activity, detail: "", after: time.Second}
	}

	for _, test := range []struct {
		name      string
		steps     []step
		activity  Activity
		authority Authority
		detail    ActivityDetail
	}{
		{
			name:      "a resting record ends a turn the hooks still call running",
			steps:     []step{hook("tool_start", ActivityRunning), terminal(ActivityIdle)},
			activity:  ActivityIdle,
			authority: AuthorityTerminal,
		},
		{
			name:      "a hook report does not replace the record",
			steps:     []step{terminal(ActivityRunning), hook("tool_end", ActivityIdle)},
			activity:  ActivityRunning,
			authority: AuthorityTerminal,
		},
		{
			name: "a held turn end outranks the resting record that follows it",
			steps: []step{
				terminal(ActivityRunning), hook(heldTurnEvent, ActivityRunning), terminal(ActivityIdle),
			},
			activity:  ActivityRunning,
			authority: AuthorityHook,
		},
		{
			name: "a held turn end outranks the resting record that came first",
			steps: []step{
				terminal(ActivityRunning), terminal(ActivityIdle), hook(heldTurnEvent, ActivityRunning),
			},
			activity:  ActivityRunning,
			authority: AuthorityHook,
		},
		{
			name: "a held turn end does not hide a blocked record",
			steps: []step{
				hook(heldTurnEvent, ActivityRunning),
				terminal(ActivityIdle),
				{
					event: "", terminal: true, activity: ActivityWaiting,
					detail: ActivityDetailPermission, after: time.Second,
				},
			},
			activity:  ActivityWaiting,
			authority: AuthorityTerminal,
			detail:    ActivityDetailPermission,
		},
		{
			name: "a held turn end that lost its lease stops holding",
			steps: []step{
				hook(heldTurnEvent, ActivityRunning),
				{
					event: "", terminal: true, activity: ActivityIdle, detail: "",
					after: IntegrationActivityLease + time.Second,
				},
			},
			activity:  ActivityIdle,
			authority: AuthorityTerminal,
		},
		{
			name: "a cleared record returns the state to the last hook report",
			steps: []step{
				terminal(ActivityRunning), hook("tool_end", ActivityIdle), terminal(ActivityUnknown),
			},
			activity:  ActivityIdle,
			authority: AuthorityHook,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := NewJournal(t.TempDir()+"/state.json", fixtureRules{})
			at := time.Date(2026, 8, 12, 6, 0, 0, 0, time.UTC)
			process := ProcessIdentity{PID: 171, StartIdentity: "boot:171", Executable: "secondary"}
			presence := PresenceLive

			steps := append([]step{hook("session_start", ActivityIdle)}, test.steps...)

			var session Session

			for _, current := range steps {
				at = at.Add(current.after)

				var evidence Evidence = &Report{
					Reporter: Reporter{Integration: "secondary-extension"},
					Event:    current.event,
					Claim:    &presence,
					Activity: &current.activity,
					Process:  &process,
				}
				if current.terminal {
					evidence = (*Reading)(&ScreenObservation{
						Activity:   current.activity,
						Detail:     current.detail,
						Authority:  AuthorityTerminal,
						Reason:     "program_status",
						Process:    process,
						ObservedAt: at,
					})
				}

				var err error

				session, err = store.Observe(context.Background(), Observation{
					Harness:  harnessSecondary,
					At:       at,
					Subject:  ObservationIdentity{SessionID: "terminal-order"},
					Evidence: evidence,
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			if session.Activity() == nil || *session.Activity() != test.activity ||
				session.Decision() == nil || session.Decision().Authority != test.authority {
				t.Fatalf("activity = %v by %#v, want %s by %s",
					session.Activity(), session.Decision(), test.activity, test.authority)
			}

			if test.detail != "" && (session.Detail == nil || session.Detail.Value != test.detail ||
				session.Detail.Quality != DetailQualityCurrent) {
				t.Fatalf("detail = %#v, want current %s", session.Detail, test.detail)
			}
		})
	}
}
