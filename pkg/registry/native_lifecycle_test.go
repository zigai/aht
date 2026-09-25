package registry_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

//nolint:cyclop // switch assertions cover both historical rows and subsequent process matching
func TestNativeSessionSwitchOnSameProcessPreservesEndedHistory(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	start := registry.NativeLifecycleStart
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	first, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionID: "old-session"}, Evidence: &registry.Report{Event: "session_start", Lifecycle: &start, Claim: &live, Activity: &idle, Process: process}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{SessionID: "new-session"}, Evidence: &registry.Report{Event: "session_start", Claim: &live, Activity: &idle, Process: process}})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.SessionID != "new-session" || second.Presence() != registry.PresenceLive {
		t.Fatalf("session switch overwrote identity: first=%#v second=%#v", first, second)
	}

	sessions, err := store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("session history count = %d, want 2: %#v", len(sessions), sessions)
	}
	for _, session := range sessions {
		switch session.SessionID {
		case "old-session":
			if session.ID != first.ID || session.Presence() != registry.PresenceGone || session.Activity() != nil {
				t.Fatalf("old session was not retired: %#v", session)
			}
		case "new-session":
			if session.ID != second.ID || session.Presence() != registry.PresenceLive {
				t.Fatalf("new session is not live: %#v", session)
			}
		default:
			t.Fatalf("unexpected session history row: %#v", session)
		}
	}

	present := true
	observed, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *process, Present: present}})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != second.ID {
		t.Fatalf("process-only observation matched %q, want current session %q", observed.ID, second.ID)
	}
}

func TestStaleNativeStartDoesNotReviveGoneSession(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	identity := registry.ObservationIdentity{SessionPath: "/tmp/pi-stale.json"}
	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	if _, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Sighting{Process: *process, Present: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(10 * time.Second), Subject: identity, Evidence: &registry.Sighting{Process: *process, Present: false}}); err != nil {
		t.Fatal(err)
	}

	start := registry.NativeLifecycleStart
	live := registry.PresenceLive
	idle := registry.ActivityIdle
	observed, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(5 * time.Second), Subject: identity, Evidence: &registry.Report{Event: "session_start", Lifecycle: &start, Claim: &live, Activity: &idle, Process: process}})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Presence() != registry.PresenceGone || observed.Activity() != nil {
		t.Fatalf("stale start revived gone session: %#v", observed)
	}
	if _, err := store.List(ctx, registry.Filter{}); err != nil {
		t.Fatalf("stale start corrupted store: %v", err)
	}
}

func TestDelayedGoneEvidenceDoesNotOverwriteNewerLivePresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initial      func(registry.ProcessIdentity, registry.ObservationIdentity, time.Time) registry.Observation
		delayed      func(registry.ProcessIdentity, registry.ObservationIdentity, time.Time) registry.Observation
		wantActivity registry.Activity
	}{
		{
			name: "process absence after native activity",
			initial: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				live := registry.PresenceLive
				idle := registry.ActivityIdle
				return registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Report{Event: "agent_settled", Claim: &live, Activity: &idle, Process: &process}}
			},
			delayed: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				return registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Sighting{Process: process, Present: false}}
			},
			wantActivity: registry.ActivityIdle,
		},
		{
			name: "native end after process presence",
			initial: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				return registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Sighting{Process: process, Present: true}}
			},
			delayed: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				end := registry.NativeLifecycleEnd
				gone := registry.PresenceGone
				return registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Report{Event: "session_end", Lifecycle: &end, Claim: &gone, Process: &process}}
			},
			wantActivity: registry.ActivityUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
			ctx := context.Background()
			at := time.Date(2026, 8, 12, 7, 0, 0, 0, time.UTC)
			identity := registry.ObservationIdentity{SessionID: "newer-live"}
			process := registry.ProcessIdentity{PID: 86, StartIdentity: "boot:86"}
			if _, err := store.Observe(ctx, test.initial(process, identity, at)); err != nil {
				t.Fatal(err)
			}

			session, err := store.Observe(ctx, test.delayed(process, identity, at.Add(-time.Second)))
			if err != nil {
				t.Fatal(err)
			}
			if session.Presence() != registry.PresenceLive || session.Activity() == nil || *session.Activity() != test.wantActivity || !session.PresenceChangedAt.Equal(at) {
				t.Fatalf("delayed gone evidence overwrote newer live state: %#v", session)
			}
		})
	}
}

func TestDelayedProcessPresenceDoesNotReviveNewerNativeGoneState(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	identity := registry.ObservationIdentity{SessionID: "newer-gone"}
	process := registry.ProcessIdentity{PID: 87, StartIdentity: "boot:87"}
	gone := registry.PresenceGone
	session, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: identity, Evidence: &registry.Report{Event: "session_end", Claim: &gone, Process: &process}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone {
		t.Fatalf("native gone state = %#v", session)
	}

	session, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(-time.Second), Subject: identity, Evidence: &registry.Sighting{Process: process, Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone || session.Activity() != nil || !session.PresenceChangedAt.Equal(at) {
		t.Fatalf("delayed process presence revived newer gone state: %#v", session)
	}
}

//nolint:gocognit,cyclop // multi-step incarnation lifecycle validates sequence, timing, and boundary transitions
func TestNativeActivityResumesDurableIdentityAcrossProcessIncarnations(t *testing.T) {
	t.Parallel()
	for _, observerFirst := range []bool{false, true} {
		name := "native_first"
		if observerFirst {
			name = "observer_first"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
			at := time.Now().UTC().Add(-time.Minute)
			identity := registry.ObservationIdentity{SessionID: "recorded-goose-session"}
			oldProcess := registry.ProcessIdentity{PID: 91, StartIdentity: "boot:91"}
			newProcess := registry.ProcessIdentity{PID: 91, StartIdentity: "boot:92"}
			ended, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: at, Subject: identity, Evidence: &registry.Report{Event: "SessionEnd", Lifecycle: new(registry.NativeLifecycleEnd), Claim: new(registry.PresenceGone), Process: &oldProcess, Listing: &registry.Listing{ResumeCommand: []string{"goose", "session", "--resume", "--session-id", identity.SessionID}}}})
			if err != nil {
				t.Fatal(err)
			}
			processObservation := registry.Observation{Harness: registry.Harness("goose"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: newProcess, Present: true}}
			if observerFirst {
				if _, err = store.Observe(ctx, processObservation); err != nil {
					t.Fatal(err)
				}
			}
			resumed, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: at.Add(2 * time.Second), Subject: identity, Evidence: &registry.Report{Event: "UserPromptSubmit", Activity: new(registry.ActivityRunning), Process: &newProcess}})
			if err != nil {
				t.Fatal(err)
			}
			if resumed.ID != ended.ID || resumed.SessionID != identity.SessionID ||
				resumed.Presence() != registry.PresenceLive || resumed.Activity() == nil || *resumed.Activity() != registry.ActivityRunning ||
				resumed.Process == nil || !resumed.Process.Equal(newProcess) {
				t.Fatalf("resume lost durable identity or running incarnation: %#v", resumed)
			}
			if resumed.Observations.Native.Event != "UserPromptSubmit" || resumed.Observations.Native.Lifecycle != nil ||
				!resumed.Observations.Native.Process.Equal(newProcess) ||
				len(resumed.ResumeCommand) != 5 || resumed.ResumeCommand[4] != identity.SessionID {
				t.Fatalf("resume changed native provenance or recorded resume target: %#v", resumed)
			}
			if !observerFirst {
				processObservation.At = at.Add(3 * time.Second)
				if _, err = store.Observe(ctx, processObservation); err != nil {
					t.Fatal(err)
				}
			}
			// The old process's delayed native callback must not rebind the
			// resumed session, even though it is newer than its original end.
			_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: at.Add(time.Second), Subject: identity, Evidence: &registry.Report{Event: "Stop", Activity: new(registry.ActivityIdle), Process: &oldProcess}})
			if !errors.Is(err, registry.ErrObservationConflict) {
				t.Fatalf("stale prior-incarnation callback error = %v, want conflict", err)
			}
			sessions, err := store.List(ctx, registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].ID != ended.ID ||
				sessions[0].Presence() != registry.PresenceLive || sessions[0].Activity() == nil ||
				*sessions[0].Activity() != registry.ActivityRunning || sessions[0].Process == nil ||
				!sessions[0].Process.Equal(newProcess) {
				t.Fatalf("resume left duplicate or stale state: %#v", sessions)
			}
		})
	}
}

//nolint:gocognit,cyclop // retirement regression validates process termination, timeline, and resurrection rejection
func TestNativeActivityCannotResurrectRetiredIncarnation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"same_process", "missing_process", "incomplete_process", "pre_end", "terminal"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
			at := time.Now().UTC().Add(-time.Minute)
			identity := registry.ObservationIdentity{SessionID: "retired-goose-session"}
			oldProcess := registry.ProcessIdentity{PID: 93, StartIdentity: "boot:93"}
			ended, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: at, Subject: identity, Evidence: &registry.Report{Event: "SessionEnd", Lifecycle: new(registry.NativeLifecycleEnd), Claim: new(registry.PresenceGone), Process: &oldProcess}})
			if err != nil {
				t.Fatal(err)
			}
			incoming := registry.Observation{Harness: registry.Harness("goose"), At: at.Add(time.Second), Subject: identity, Evidence: &registry.Report{Event: "UserPromptSubmit", Activity: new(registry.ActivityRunning), Process: &registry.ProcessIdentity{PID: 94, StartIdentity: "boot:94"}}}
			switch name {
			case "same_process":
				incoming.SetProcess(&oldProcess)
			case "missing_process":
				incoming.SetProcess(nil)
			case "incomplete_process":
				incoming.ProcessIdentity().StartIdentity = ""
			case "pre_end":
				incoming.At = at.Add(-time.Second)
			case "terminal":
				incoming.Report().Event = "SessionEnd"
				incoming.SetActivity(nil)
				incoming.Report().Lifecycle = new(registry.NativeLifecycleEnd)
				incoming.Report().Claim = new(registry.PresenceGone)
			}
			_, err = store.Observe(ctx, incoming)
			if name == "incomplete_process" && err == nil {
				t.Fatal("incomplete process identity was accepted")
			}
			if name != "incomplete_process" && err != nil {
				t.Fatal(err)
			}
			session, err := store.Get(ctx, ended.ID)
			if err != nil {
				t.Fatal(err)
			}
			if session.Presence() != registry.PresenceGone || session.Activity() != nil ||
				session.Process == nil || !session.Process.Equal(oldProcess) ||
				session.Observations.Native.Event != "SessionEnd" || !session.PresenceChangedAt.Equal(at) {
				t.Fatalf("post-end callback resurrected or changed terminal evidence: %#v", session)
			}
		})
	}
}

//nolint:cyclop // generation test covers terminal, ignored, resume, and process transitions
func TestV2NativeTerminalAndResumeReduction(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	base := time.Now().UTC().Add(-time.Minute)
	start := registry.NativeLifecycleStart
	idle := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base, Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Report{Event: "test", Lifecycle: &start, Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceUnknown || session.Activity() == nil || *session.Activity() != registry.ActivityIdle {
		t.Fatalf("start reduction: %#v", session)
	}
	end := registry.NativeLifecycleEnd
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base.Add(time.Second), Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Report{Event: "test", Lifecycle: &end}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone || session.Activity() != nil {
		t.Fatalf("end reduction: %#v", session)
	}
	processPresent := true
	process := &registry.ProcessIdentity{PID: 12, StartIdentity: "boot:12"}
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base.Add(1500 * time.Millisecond), Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Sighting{Process: *process, Present: processPresent}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone {
		t.Fatalf("post-terminal process revived session: %#v", session)
	}
	running := registry.ActivityRunning
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base.Add(2 * time.Second), Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Report{Event: "test", Activity: &running}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceGone || session.Activity() != nil || session.Observations.Native.Lifecycle == nil || *session.Observations.Native.Lifecycle != registry.NativeLifecycleEnd {
		t.Fatalf("post-terminal activity changed evidence: %#v", session)
	}
	resume := registry.NativeLifecycleResume
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base.Add(3 * time.Second), Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Report{Event: "test", Lifecycle: &resume}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceUnknown {
		t.Fatalf("resume should await process evidence: %#v", session)
	}
	session, err = store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("pi"), At: base.Add(4 * time.Second), Subject: registry.ObservationIdentity{SessionID: "s"}, Evidence: &registry.Sighting{Process: *process, Present: processPresent}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence() != registry.PresenceLive {
		t.Fatalf("process did not bind resumed generation: %#v", session)
	}
}
