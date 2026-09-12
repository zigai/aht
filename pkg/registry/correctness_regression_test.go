package registry

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestIdentityReconciliationPrefersSessionPath(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	first, err := store.Observe(context.Background(), Observation{Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: HarnessClaude, Identity: ObservationIdentity{SessionPath: "/tmp/session.json"}, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(context.Background(), Observation{Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence, Harness: HarnessClaude, Identity: ObservationIdentity{SessionPath: "/tmp/session.json"}, ProcessPresent: new(true), Process: &ProcessIdentity{PID: 41, StartIdentity: "boot:41"}, ObservedAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Presence != PresenceLive {
		t.Fatalf("observations did not reconcile: first=%#v second=%#v", first, second)
	}
}

//nolint:cyclop // reconciliation assertions cover identity, process, location, and row compaction
func TestNativeProcessIdentityReconcilesWithLiveTmuxSession(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	path := "/tmp/pi-session.json"
	process := &ProcessIdentity{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/node", CWD: "/work", TTY: "/dev/pts/4"}
	idle := ActivityIdle
	identityOnly, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: ObservationIdentity{SessionPath: path},
		NativeEvent: "session_start", Activity: &idle, ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, ProcessPresent: new(true), Process: process, ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	tmux := &TmuxContext{Inside: true, SessionName: "sesh", PaneID: "%81", PaneTTY: "/dev/pts/4"}
	if _, err := store.Observe(ctx, Observation{
		Source: ObservationSourceTmux, Evidence: ObservationEvidenceTmuxLocation,
		Harness: HarnessPi, Process: process, Tmux: tmux, ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	running := ActivityRunning
	reconciled, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: ObservationIdentity{SessionPath: path}, Process: process,
		NativeEvent: "agent_start", Activity: &running, ObservedAt: at.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.ID != live.ID || reconciled.ID == identityOnly.ID {
		t.Fatalf("native report reconciled to %q, want live process record %q (identity-only %q)", reconciled.ID, live.ID, identityOnly.ID)
	}
	if reconciled.Presence != PresenceLive || reconciled.Activity == nil || *reconciled.Activity != ActivityRunning || reconciled.SessionPath != path || reconciled.Tmux.PaneID != "%81" {
		t.Fatalf("reconciled session lost identity, activity, or tmux location: %#v", reconciled)
	}
	sessions, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != live.ID {
		t.Fatalf("provisional identity row survived reconciliation: %#v", sessions)
	}
}

//nolint:cyclop // switch assertions cover both historical rows and subsequent process matching
func TestNativeSessionSwitchOnSameProcessPreservesEndedHistory(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	start := NativeLifecycleStart
	live := PresenceLive
	idle := ActivityIdle

	first, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: ObservationIdentity{SessionID: "old-session"},
		Lifecycle: &start, Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: ObservationIdentity{SessionID: "new-session"},
		Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.SessionID != "new-session" || second.Presence != PresenceLive {
		t.Fatalf("session switch overwrote identity: first=%#v second=%#v", first, second)
	}

	sessions, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("session history count = %d, want 2: %#v", len(sessions), sessions)
	}
	for _, session := range sessions {
		switch session.SessionID {
		case "old-session":
			if session.ID != first.ID || session.Presence != PresenceGone || session.Activity != nil {
				t.Fatalf("old session was not retired: %#v", session)
			}
		case "new-session":
			if session.ID != second.ID || session.Presence != PresenceLive {
				t.Fatalf("new session is not live: %#v", session)
			}
		default:
			t.Fatalf("unexpected session history row: %#v", session)
		}
	}

	present := true
	observed, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != second.ID {
		t.Fatalf("process-only observation matched %q, want current session %q", observed.ID, second.ID)
	}
}

func TestProcessObservationRetiresDifferentHarnessWithSameProcess(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	live := PresenceLive
	idle := ActivityIdle

	openCode, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessOpenCode, Identity: ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}

	present := true
	omp, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessOmp, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if omp.Harness != HarnessOmp || omp.Presence != PresenceLive {
		t.Fatalf("OMP process session is not live: %#v", omp)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Harness != HarnessOpenCode || openCode.Presence != PresenceGone || openCode.Activity != nil {
		t.Fatalf("OpenCode session was not retired: %#v", openCode)
	}

	sessions, err := store.List(ctx, Filter{Presence: PresenceLive})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != omp.ID {
		t.Fatalf("live sessions = %#v, want only OMP %q", sessions, omp.ID)
	}
}

func TestTmuxObservationRetiresDifferentHarnessWithoutProcessIdentity(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &TmuxContext{
		Inside: true, ServerSocket: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := PresenceLive
	idle := ActivityIdle

	openCode, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessOpenCode, Identity: ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Activity: &idle, Tmux: location, NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Process != nil {
		t.Fatalf("fixture unexpectedly has process identity: %#v", openCode)
	}

	process := &ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	present := true
	if _, err = store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessOmp, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	omp, err := store.Observe(ctx, Observation{
		Source: ObservationSourceTmux, Evidence: ObservationEvidenceTmuxLocation,
		Harness: HarnessOmp, Process: process, Tmux: location,
		ObservedAt: at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence != PresenceGone || openCode.Activity != nil || !openCode.ActivityChangedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("processless OpenCode session was not retired at the replacement time: %#v", openCode)
	}
	if omp.Harness != HarnessOmp || omp.Presence != PresenceLive || omp.Tmux.PaneID != location.PaneID {
		t.Fatalf("OMP replacement is not live on the pane: %#v", omp)
	}
}

func TestTmuxObservationPreservesDifferentLiveProcessOnSamePane(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &TmuxContext{
		Inside: true, ServerSocket: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := PresenceLive
	openCodeProcess := &ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	ompProcess := &ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	openCode, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessOpenCode, Identity: ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Process: openCodeProcess, Tmux: location,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	present := true
	if _, err = store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessOpenCode, ProcessPresent: &present, Process: openCodeProcess,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessOmp, ProcessPresent: &present, Process: ompProcess,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, Observation{
		Source: ObservationSourceTmux, Evidence: ObservationEvidenceTmuxLocation,
		Harness: HarnessOmp, Process: ompProcess, Tmux: location,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence != PresenceLive || openCode.Process == nil || !openCode.Process.Equal(*openCodeProcess) {
		t.Fatalf("distinct live OpenCode process was retired: %#v", openCode)
	}
}

func TestNativeProcessIdentitySeedsObserverReconciliation(t *testing.T) {
	t.Parallel()
	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	tmux := &TmuxContext{Inside: true, SessionName: "dev", PaneID: "%4"}
	activity := ActivityRunning
	native, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: ObservationIdentity{SessionPath: "/tmp/pi-session.json"},
		Process: process, Tmux: tmux, NativeEvent: "agent_start", Activity: &activity, ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, ProcessPresent: new(true), Process: process, ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != native.ID || observed.Presence != PresenceLive || observed.Activity == nil || *observed.Activity != ActivityRunning || observed.Tmux.PaneID != "%4" {
		t.Fatalf("observer did not reconcile with native process identity: native=%#v observed=%#v", native, observed)
	}
}

func TestStaleNativeStartDoesNotReviveGoneSession(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	identity := ObservationIdentity{SessionPath: "/tmp/pi-stale.json"}
	process := &ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	if _, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, Identity: identity, ProcessPresent: new(true), Process: process,
		ObservedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, Identity: identity, ProcessPresent: new(false), Process: process,
		ObservedAt: at.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	start := NativeLifecycleStart
	live := PresenceLive
	idle := ActivityIdle
	observed, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: identity, Lifecycle: &start, Presence: &live, Activity: &idle,
		Process: process, NativeEvent: "session_start", ObservedAt: at.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.Presence != PresenceGone || observed.Activity != nil {
		t.Fatalf("stale start revived gone session: %#v", observed)
	}
	if _, err := store.List(ctx, Filter{}); err != nil {
		t.Fatalf("stale start corrupted store: %v", err)
	}
}

func TestDelayedGoneEvidenceDoesNotOverwriteNewerLivePresence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initial      func(ProcessIdentity, ObservationIdentity, time.Time) Observation
		delayed      func(ProcessIdentity, ObservationIdentity, time.Time) Observation
		wantActivity Activity
	}{
		{
			name: "process absence after native activity",
			initial: func(process ProcessIdentity, identity ObservationIdentity, at time.Time) Observation {
				live := PresenceLive
				idle := ActivityIdle
				return Observation{
					Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
					Harness: HarnessPi, Identity: identity, Presence: &live, Activity: &idle,
					Process: &process, NativeEvent: "agent_settled", ObservedAt: at,
				}
			},
			delayed: func(process ProcessIdentity, identity ObservationIdentity, at time.Time) Observation {
				return Observation{
					Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
					Harness: HarnessPi, Identity: identity, ProcessPresent: new(false), Process: &process,
					ObservedAt: at,
				}
			},
			wantActivity: ActivityIdle,
		},
		{
			name: "native end after process presence",
			initial: func(process ProcessIdentity, identity ObservationIdentity, at time.Time) Observation {
				return Observation{
					Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
					Harness: HarnessPi, Identity: identity, ProcessPresent: new(true), Process: &process,
					ObservedAt: at,
				}
			},
			delayed: func(process ProcessIdentity, identity ObservationIdentity, at time.Time) Observation {
				end := NativeLifecycleEnd
				gone := PresenceGone
				return Observation{
					Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
					Harness: HarnessPi, Identity: identity, Lifecycle: &end, Presence: &gone,
					Process: &process, NativeEvent: "session_end", ObservedAt: at,
				}
			},
			wantActivity: ActivityUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
			ctx := context.Background()
			at := time.Date(2026, 8, 12, 7, 0, 0, 0, time.UTC)
			identity := ObservationIdentity{SessionID: "newer-live"}
			process := ProcessIdentity{PID: 86, StartIdentity: "boot:86"}
			if _, err := store.Observe(ctx, test.initial(process, identity, at)); err != nil {
				t.Fatal(err)
			}

			session, err := store.Observe(ctx, test.delayed(process, identity, at.Add(-time.Second)))
			if err != nil {
				t.Fatal(err)
			}
			if session.Presence != PresenceLive || session.Activity == nil || *session.Activity != test.wantActivity || !session.PresenceChangedAt.Equal(at) {
				t.Fatalf("delayed gone evidence overwrote newer live state: %#v", session)
			}
		})
	}
}

func TestDelayedProcessPresenceDoesNotReviveNewerNativeGoneState(t *testing.T) {
	t.Parallel()

	store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	identity := ObservationIdentity{SessionID: "newer-gone"}
	process := ProcessIdentity{PID: 87, StartIdentity: "boot:87"}
	gone := PresenceGone
	session, err := store.Observe(ctx, Observation{
		Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
		Harness: HarnessPi, Identity: identity, Presence: &gone, Process: &process,
		NativeEvent: "session_end", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != PresenceGone {
		t.Fatalf("native gone state = %#v", session)
	}

	session, err = store.Observe(ctx, Observation{
		Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
		Harness: HarnessPi, Identity: identity, ProcessPresent: new(true), Process: &process,
		ObservedAt: at.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Presence != PresenceGone || session.Activity != nil || !session.PresenceChangedAt.Equal(at) {
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
			store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
			at := time.Now().UTC().Add(-time.Minute)
			identity := ObservationIdentity{SessionID: "recorded-goose-session"}
			oldProcess := ProcessIdentity{PID: 91, StartIdentity: "boot:91"}
			newProcess := ProcessIdentity{PID: 91, StartIdentity: "boot:92"}
			ended, err := store.Observe(ctx, Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "SessionEnd", Lifecycle: new(NativeLifecycleEnd),
				Presence: new(PresenceGone), ObservedAt: at,
				Catalog: &CatalogMetadata{ResumeCommand: []string{"goose", "session", "--resume", "--session-id", identity.SessionID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			processObservation := Observation{
				Source: ObservationSourceProcess, Evidence: ObservationEvidenceProcessPresence,
				Harness: HarnessGoose, Process: &newProcess, ProcessPresent: new(true),
				ObservedAt: at.Add(time.Second),
			}
			if observerFirst {
				if _, err = store.Observe(ctx, processObservation); err != nil {
					t.Fatal(err)
				}
			}
			resumed, err := store.Observe(ctx, Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessGoose, Identity: identity, Process: &newProcess,
				NativeEvent: "UserPromptSubmit", Activity: new(ActivityRunning),
				ObservedAt: at.Add(2 * time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			if resumed.ID != ended.ID || resumed.SessionID != identity.SessionID ||
				resumed.Presence != PresenceLive || resumed.Activity == nil || *resumed.Activity != ActivityRunning ||
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
			_, err = store.Observe(ctx, Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "Stop", Activity: new(ActivityIdle),
				ObservedAt: at.Add(time.Second),
			})
			if !errors.Is(err, ErrObservationConflict) {
				t.Fatalf("stale prior-incarnation callback error = %v, want conflict", err)
			}
			sessions, err := store.List(ctx, Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].ID != ended.ID ||
				sessions[0].Presence != PresenceLive || sessions[0].Activity == nil ||
				*sessions[0].Activity != ActivityRunning || sessions[0].Process == nil ||
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
			store := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
			at := time.Now().UTC().Add(-time.Minute)
			identity := ObservationIdentity{SessionID: "retired-goose-session"}
			oldProcess := ProcessIdentity{PID: 93, StartIdentity: "boot:93"}
			ended, err := store.Observe(ctx, Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessGoose, Identity: identity, Process: &oldProcess,
				NativeEvent: "SessionEnd", Lifecycle: new(NativeLifecycleEnd),
				Presence: new(PresenceGone), ObservedAt: at,
			})
			if err != nil {
				t.Fatal(err)
			}
			incoming := Observation{
				Source: ObservationSourceNative, Evidence: ObservationEvidenceNativeEvent,
				Harness: HarnessGoose, Identity: identity,
				Process:     &ProcessIdentity{PID: 94, StartIdentity: "boot:94"},
				NativeEvent: "UserPromptSubmit", Activity: new(ActivityRunning),
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
				incoming.Lifecycle = new(NativeLifecycleEnd)
				incoming.Presence = new(PresenceGone)
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
			if session.Presence != PresenceGone || session.Activity != nil ||
				session.Process == nil || !session.Process.Equal(oldProcess) ||
				session.Observations.Native.Event != "SessionEnd" || !session.PresenceChangedAt.Equal(at) {
				t.Fatalf("post-end callback resurrected or changed terminal evidence: %#v", session)
			}
		})
	}
}
