package registry_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestIdentityReconciliationPrefersSessionPath(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	at := time.Now().UTC().Add(-time.Minute)
	first, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: at, Subject: registry.ObservationIdentity{SessionPath: "/tmp/session.json"}, Evidence: &registry.Report{Event: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("claude"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{SessionPath: "/tmp/session.json"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 41, StartIdentity: "boot:41"}, Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Presence() != registry.PresenceLive {
		t.Fatalf("observations did not reconcile: first=%#v second=%#v", first, second)
	}
}

//nolint:cyclop // reconciliation assertions cover identity, process, location, and row compaction
func TestNativeProcessIdentityReconcilesWithLiveTmuxSession(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	path := "/tmp/pi-session.json"
	process := &registry.ProcessIdentity{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/node", CWD: "/work", TTY: "/dev/pts/4"}
	idle := registry.ActivityIdle
	identityOnly, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionPath: path}, Evidence: &registry.Report{Event: "session_start", Activity: &idle}})
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *process, Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	tmux := &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "sesh", PaneID: "%81", PaneTTY: "/dev/pts/4"}
	if _, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Placement{Process: *process, Location: *tmux}}); err != nil {
		t.Fatal(err)
	}
	running := registry.ActivityRunning
	reconciled, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(3 * time.Second), Subject: registry.ObservationIdentity{SessionPath: path}, Evidence: &registry.Report{Event: "agent_start", Activity: &running, Process: process}})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.ID != live.ID || reconciled.ID == identityOnly.ID {
		t.Fatalf("native report reconciled to %q, want live process record %q (identity-only %q)", reconciled.ID, live.ID, identityOnly.ID)
	}
	if reconciled.Presence() != registry.PresenceLive || reconciled.Activity() == nil || *reconciled.Activity() != registry.ActivityRunning || reconciled.SessionPath != path || reconciled.Location.PaneID != "%81" {
		t.Fatalf("reconciled session lost identity, activity, or tmux location: %#v", reconciled)
	}
	sessions, err := store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != live.ID {
		t.Fatalf("provisional identity row survived reconciliation: %#v", sessions)
	}
}

func TestProcessObservationRetiresDifferentHarnessWithSameProcess(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	openCode, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("opencode"), At: at, Subject: registry.ObservationIdentity{SessionID: "opencode-session"}, Evidence: &registry.Report{Event: "session_start", Claim: &live, Activity: &idle, Process: process}})
	if err != nil {
		t.Fatal(err)
	}

	present := true
	omp, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("omp"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *process, Present: present}})
	if err != nil {
		t.Fatal(err)
	}
	if omp.Harness != registry.Harness("omp") || omp.Presence() != registry.PresenceLive {
		t.Fatalf("OMP process session is not live: %#v", omp)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Harness != registry.Harness("opencode") || openCode.Presence() != registry.PresenceGone || openCode.Activity() != nil {
		t.Fatalf("OpenCode session was not retired: %#v", openCode)
	}

	sessions, err := store.List(ctx, registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != omp.ID {
		t.Fatalf("live sessions = %#v, want only OMP %q", sessions, omp.ID)
	}
}

func TestMultiplexerObservationRetiresDifferentHarnessWithoutProcessIdentity(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &registry.Location{
		Kind: registry.MultiplexerTmux, ServerID: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	openCode, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("opencode"), At: at, Subject: registry.ObservationIdentity{SessionID: "opencode-session"}, Evidence: &registry.Report{Event: "session_start", Claim: &live, Activity: &idle, Location: location}})
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Process != nil {
		t.Fatalf("fixture unexpectedly has process identity: %#v", openCode)
	}

	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	present := true
	if _, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("omp"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *process, Present: present}}); err != nil {
		t.Fatal(err)
	}
	omp, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("omp"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Placement{Process: *process, Location: *location}})
	if err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence() != registry.PresenceGone || openCode.Activity() != nil || !openCode.ActivityChangedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("processless OpenCode session was not retired at the replacement time: %#v", openCode)
	}
	if omp.Harness != registry.Harness("omp") || omp.Presence() != registry.PresenceLive || omp.Location.PaneID != location.PaneID {
		t.Fatalf("OMP replacement is not live on the pane: %#v", omp)
	}
}

func TestMultiplexerObservationPreservesDifferentLiveProcessOnSamePane(t *testing.T) {
	t.Parallel()

	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &registry.Location{
		Kind: registry.MultiplexerTmux, ServerID: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := registry.PresenceLive
	openCodeProcess := &registry.ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	ompProcess := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	openCode, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("opencode"), At: at, Subject: registry.ObservationIdentity{SessionID: "opencode-session"}, Evidence: &registry.Report{Event: "session_start", Claim: &live, Process: openCodeProcess, Location: location}})
	if err != nil {
		t.Fatal(err)
	}
	present := true
	if _, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("opencode"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *openCodeProcess, Present: present}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("omp"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *ompProcess, Present: present}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("omp"), At: at.Add(2 * time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Placement{Process: *ompProcess, Location: *location}}); err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence() != registry.PresenceLive || openCode.Process == nil || !openCode.Process.Equal(*openCodeProcess) {
		t.Fatalf("distinct live OpenCode process was retired: %#v", openCode)
	}
}

func TestNativeProcessIdentitySeedsObserverReconciliation(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	tmux := &registry.Location{Kind: registry.MultiplexerTmux, SessionName: "dev", PaneID: "%4"}
	activity := registry.ActivityRunning
	native, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at, Subject: registry.ObservationIdentity{SessionPath: "/tmp/pi-session.json"}, Evidence: &registry.Report{Event: "agent_start", Activity: &activity, Process: process, Location: tmux}})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: at.Add(time.Second), Subject: registry.ObservationIdentity{}, Evidence: &registry.Sighting{Process: *process, Present: true}})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != native.ID || observed.Presence() != registry.PresenceLive || observed.Activity() == nil || *observed.Activity() != registry.ActivityRunning || observed.Location.PaneID != "%4" {
		t.Fatalf("observer did not reconcile with native process identity: native=%#v observed=%#v", native, observed)
	}
}
