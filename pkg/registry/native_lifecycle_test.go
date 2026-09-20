package registry_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

//nolint:cyclop // switch assertions cover both historical rows and subsequent process matching
func TestNativeSessionSwitchOnSameProcessPreservesEndedHistory(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	start := registry.NativeLifecycleStart
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	first, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionID: "old-session"},
		Lifecycle: &start, Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionID: "new-session"},
		Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.SessionID != "new-session" || second.Presence != registry.PresenceLive {
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
			if session.ID != first.ID || session.Presence != registry.PresenceGone || session.Activity != nil {
				t.Fatalf("old session was not retired: %#v", session)
			}
		case "new-session":
			if session.ID != second.ID || session.Presence != registry.PresenceLive {
				t.Fatalf("new session is not live: %#v", session)
			}
		default:
			t.Fatalf("unexpected session history row: %#v", session)
		}
	}

	present := true
	observed, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != second.ID {
		t.Fatalf("process-only observation matched %q, want current session %q", observed.ID, second.ID)
	}
}

func TestStaleNativeStartDoesNotReviveGoneSession(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	identity := registry.ObservationIdentity{SessionPath: "/tmp/pi-stale.json"}
	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	if _, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, Identity: identity, ProcessPresent: new(true), Process: process,
		ObservedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, Identity: identity, ProcessPresent: new(false), Process: process,
		ObservedAt: at.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	start := registry.NativeLifecycleStart
	live := registry.PresenceLive
	idle := registry.ActivityIdle
	observed, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: identity, Lifecycle: &start, Presence: &live, Activity: &idle,
		Process: process, NativeEvent: "session_start", ObservedAt: at.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Presence != registry.PresenceGone || observed.Activity != nil {
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
				return registry.Observation{
					Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
					Harness: registry.HarnessPi, Identity: identity, Presence: &live, Activity: &idle,
					Process: &process, NativeEvent: "agent_settled", ObservedAt: at,
				}
			},
			delayed: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				return registry.Observation{
					Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
					Harness: registry.HarnessPi, Identity: identity, ProcessPresent: new(false), Process: &process,
					ObservedAt: at,
				}
			},
			wantActivity: registry.ActivityIdle,
		},
		{
			name: "native end after process presence",
			initial: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				return registry.Observation{
					Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
					Harness: registry.HarnessPi, Identity: identity, ProcessPresent: new(true), Process: &process,
					ObservedAt: at,
				}
			},
			delayed: func(process registry.ProcessIdentity, identity registry.ObservationIdentity, at time.Time) registry.Observation {
				end := registry.NativeLifecycleEnd
				gone := registry.PresenceGone
				return registry.Observation{
					Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
					Harness: registry.HarnessPi, Identity: identity, Lifecycle: &end, Presence: &gone,
					Process: &process, NativeEvent: "session_end", ObservedAt: at,
				}
			},
			wantActivity: registry.ActivityUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
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
			if session.Presence != registry.PresenceLive || session.Activity == nil || *session.Activity != test.wantActivity || !session.PresenceChangedAt.Equal(at) {
				t.Fatalf("delayed gone evidence overwrote newer live state: %#v", session)
			}
		})
	}
}

func TestDelayedProcessPresenceDoesNotReviveNewerNativeGoneState(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	identity := registry.ObservationIdentity{SessionID: "newer-gone"}
	process := registry.ProcessIdentity{PID: 87, StartIdentity: "boot:87"}
	gone := registry.PresenceGone
	session, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: identity, Presence: &gone, Process: &process,
		NativeEvent: "session_end", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone {
		t.Fatalf("native gone state = %#v", session)
	}

	session, err = store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, Identity: identity, ProcessPresent: new(true), Process: &process,
		ObservedAt: at.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone || session.Activity != nil || !session.PresenceChangedAt.Equal(at) {
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
			store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
			at := time.Now().UTC().Add(-time.Minute)
			identity := registry.ObservationIdentity{SessionID: "recorded-goose-session"}
			oldProcess := registry.ProcessIdentity{PID: 91, StartIdentity: "boot:91"}
			newProcess := registry.ProcessIdentity{PID: 91, StartIdentity: "boot:92"}
			ended, err := store.Observe(ctx, registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "SessionEnd", Lifecycle: new(registry.NativeLifecycleEnd),
				Presence: new(registry.PresenceGone), ObservedAt: at,
				Catalog: &registry.CatalogMetadata{ResumeCommand: []string{"goose", "session", "--resume", "--session-id", identity.SessionID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			processObservation := registry.Observation{
				Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
				Harness: registry.HarnessGoose, Process: &newProcess, ProcessPresent: new(true),
				ObservedAt: at.Add(time.Second),
			}
			if observerFirst {
				if _, err = store.Observe(ctx, processObservation); err != nil {
					t.Fatal(err)
				}
			}
			resumed, err := store.Observe(ctx, registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessGoose, Identity: identity, Process: &newProcess,
				NativeEvent: "UserPromptSubmit", Activity: new(registry.ActivityRunning),
				ObservedAt: at.Add(2 * time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			if resumed.ID != ended.ID || resumed.SessionID != identity.SessionID ||
				resumed.Presence != registry.PresenceLive || resumed.Activity == nil || *resumed.Activity != registry.ActivityRunning ||
				resumed.Process == nil || !resumed.Process.Equal(newProcess) {
				t.Fatalf("resume lost durable identity or running incarnation: %#v", resumed)
			}
			if resumed.Observations.Native.Event != "UserPromptSubmit" || resumed.Observations.Native.Lifecycle != nil ||
				!resumed.Observations.Native.Process.Equal(newProcess) ||
				len(resumed.ResumeCommand) != 5 || resumed.ResumeCommand[4] != identity.SessionID {
				t.Fatalf("resume changed native provenance or recorded resume target: %#v", resumed)
			}
			if !observerFirst {
				processObservation.ObservedAt = at.Add(3 * time.Second)
				if _, err = store.Observe(ctx, processObservation); err != nil {
					t.Fatal(err)
				}
			}
			// The old process's delayed native callback must not rebind the
			// resumed session, even though it is newer than its original end.
			_, err = store.Observe(ctx, registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "Stop", Activity: new(registry.ActivityIdle),
				ObservedAt: at.Add(time.Second),
			})
			if !errors.Is(err, registry.ErrObservationConflict) {
				t.Fatalf("stale prior-incarnation callback error = %v, want conflict", err)
			}
			sessions, err := store.List(ctx, registry.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].ID != ended.ID ||
				sessions[0].Presence != registry.PresenceLive || sessions[0].Activity == nil ||
				*sessions[0].Activity != registry.ActivityRunning || sessions[0].Process == nil ||
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
			store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
			at := time.Now().UTC().Add(-time.Minute)
			identity := registry.ObservationIdentity{SessionID: "retired-goose-session"}
			oldProcess := registry.ProcessIdentity{PID: 93, StartIdentity: "boot:93"}
			ended, err := store.Observe(ctx, registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "SessionEnd", Lifecycle: new(registry.NativeLifecycleEnd),
				Presence: new(registry.PresenceGone), ObservedAt: at,
			})
			if err != nil {
				t.Fatal(err)
			}
			incoming := registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessGoose, Identity: identity,
				Process:     &registry.ProcessIdentity{PID: 94, StartIdentity: "boot:94"},
				NativeEvent: "UserPromptSubmit", Activity: new(registry.ActivityRunning),
				ObservedAt: at.Add(time.Second),
			}
			switch name {
			case "same_process":
				incoming.Process = &oldProcess
			case "missing_process":
				incoming.Process = nil
			case "incomplete_process":
				incoming.Process.StartIdentity = ""
			case "pre_end":
				incoming.ObservedAt = at.Add(-time.Second)
			case "terminal":
				incoming.NativeEvent = "SessionEnd"
				incoming.Activity = nil
				incoming.Lifecycle = new(registry.NativeLifecycleEnd)
				incoming.Presence = new(registry.PresenceGone)
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
			if session.Presence != registry.PresenceGone || session.Activity != nil ||
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
	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	base := time.Now().UTC().Add(-time.Minute)
	start := registry.NativeLifecycleStart
	idle := registry.ActivityIdle
	session, err := store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, Lifecycle: &start, Activity: &idle, ObservedAt: base})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceUnknown || session.Activity == nil || *session.Activity != registry.ActivityIdle {
		t.Fatalf("start reduction: %#v", session)
	}
	end := registry.NativeLifecycleEnd
	session, err = store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, Lifecycle: &end, ObservedAt: base.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone || session.Activity != nil {
		t.Fatalf("end reduction: %#v", session)
	}
	processPresent := true
	process := &registry.ProcessIdentity{PID: 12, StartIdentity: "boot:12"}
	session, err = store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence, Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, ProcessPresent: &processPresent, Process: process, ObservedAt: base.Add(1500 * time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone {
		t.Fatalf("post-terminal process revived session: %#v", session)
	}
	running := registry.ActivityRunning
	session, err = store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, Activity: &running, ObservedAt: base.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceGone || session.Activity != nil || session.Observations.Native.Lifecycle == nil || *session.Observations.Native.Lifecycle != registry.NativeLifecycleEnd {
		t.Fatalf("post-terminal activity changed evidence: %#v", session)
	}
	resume := registry.NativeLifecycleResume
	session, err = store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, Lifecycle: &resume, ObservedAt: base.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceUnknown {
		t.Fatalf("resume should await process evidence: %#v", session)
	}
	session, err = store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence, Harness: registry.HarnessCodex, Identity: registry.ObservationIdentity{SessionID: "s"}, ProcessPresent: &processPresent, Process: process, ObservedAt: base.Add(4 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != registry.PresenceLive {
		t.Fatalf("process did not bind resumed generation: %#v", session)
	}
}
