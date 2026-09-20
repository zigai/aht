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
	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	at := time.Now().UTC().Add(-time.Minute)
	first, err := store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent, NativeEvent: "test", Harness: registry.HarnessClaude, Identity: registry.ObservationIdentity{SessionPath: "/tmp/session.json"}, ObservedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Observe(context.Background(), registry.Observation{Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence, Harness: registry.HarnessClaude, Identity: registry.ObservationIdentity{SessionPath: "/tmp/session.json"}, ProcessPresent: new(true), Process: &registry.ProcessIdentity{PID: 41, StartIdentity: "boot:41"}, ObservedAt: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Presence != registry.PresenceLive {
		t.Fatalf("observations did not reconcile: first=%#v second=%#v", first, second)
	}
}

//nolint:cyclop // reconciliation assertions cover identity, process, location, and row compaction
func TestNativeProcessIdentityReconcilesWithLiveTmuxSession(t *testing.T) {
	t.Parallel()
	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	path := "/tmp/pi-session.json"
	process := &registry.ProcessIdentity{PID: 42, PPID: 10, ProcessGroupID: 42, StartIdentity: "boot:42", Executable: "/usr/bin/node", CWD: "/work", TTY: "/dev/pts/4"}
	idle := registry.ActivityIdle
	identityOnly, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionPath: path},
		NativeEvent: "session_start", Activity: &idle, ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, ProcessPresent: new(true), Process: process, ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	tmux := &registry.TmuxContext{Inside: true, SessionName: "sesh", PaneID: "%81", PaneTTY: "/dev/pts/4"}
	if _, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceTmux, Evidence: registry.ObservationEvidenceTmuxLocation,
		Harness: registry.HarnessPi, Process: process, Tmux: tmux, ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	running := registry.ActivityRunning
	reconciled, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionPath: path}, Process: process,
		NativeEvent: "agent_start", Activity: &running, ObservedAt: at.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.ID != live.ID || reconciled.ID == identityOnly.ID {
		t.Fatalf("native report reconciled to %q, want live process record %q (identity-only %q)", reconciled.ID, live.ID, identityOnly.ID)
	}
	if reconciled.Presence != registry.PresenceLive || reconciled.Activity == nil || *reconciled.Activity != registry.ActivityRunning || reconciled.SessionPath != path || reconciled.Tmux.PaneID != "%81" {
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

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	openCode, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessOpenCode, Identity: registry.ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Activity: &idle, Process: process,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}

	present := true
	omp, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessOmp, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if omp.Harness != registry.HarnessOmp || omp.Presence != registry.PresenceLive {
		t.Fatalf("OMP process session is not live: %#v", omp)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Harness != registry.HarnessOpenCode || openCode.Presence != registry.PresenceGone || openCode.Activity != nil {
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

func TestTmuxObservationRetiresDifferentHarnessWithoutProcessIdentity(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &registry.TmuxContext{
		Inside: true, ServerSocket: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := registry.PresenceLive
	idle := registry.ActivityIdle

	openCode, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessOpenCode, Identity: registry.ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Activity: &idle, Tmux: location, NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Process != nil {
		t.Fatalf("fixture unexpectedly has process identity: %#v", openCode)
	}

	process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}
	present := true
	if _, err = store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessOmp, ProcessPresent: &present, Process: process,
		ObservedAt: at.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	omp, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceTmux, Evidence: registry.ObservationEvidenceTmuxLocation,
		Harness: registry.HarnessOmp, Process: process, Tmux: location,
		ObservedAt: at.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence != registry.PresenceGone || openCode.Activity != nil || !openCode.ActivityChangedAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("processless OpenCode session was not retired at the replacement time: %#v", openCode)
	}
	if omp.Harness != registry.HarnessOmp || omp.Presence != registry.PresenceLive || omp.Tmux.PaneID != location.PaneID {
		t.Fatalf("OMP replacement is not live on the pane: %#v", omp)
	}
}

func TestTmuxObservationPreservesDifferentLiveProcessOnSamePane(t *testing.T) {
	t.Parallel()

	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	location := &registry.TmuxContext{
		Inside: true, ServerSocket: "/tmp/tmux/default", SessionID: "$0",
		SessionName: "0", WindowID: "@2", PaneID: "%3",
	}
	live := registry.PresenceLive
	openCodeProcess := &registry.ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	ompProcess := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	openCode, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessOpenCode, Identity: registry.ObservationIdentity{SessionID: "opencode-session"},
		Presence: &live, Process: openCodeProcess, Tmux: location,
		NativeEvent: "session_start", ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	present := true
	if _, err = store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessOpenCode, ProcessPresent: &present, Process: openCodeProcess,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessOmp, ProcessPresent: &present, Process: ompProcess,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceTmux, Evidence: registry.ObservationEvidenceTmuxLocation,
		Harness: registry.HarnessOmp, Process: ompProcess, Tmux: location,
		ObservedAt: at.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	openCode, err = store.Get(ctx, openCode.ID)
	if err != nil {
		t.Fatal(err)
	}
	if openCode.Presence != registry.PresenceLive || openCode.Process == nil || !openCode.Process.Equal(*openCodeProcess) {
		t.Fatalf("distinct live OpenCode process was retired: %#v", openCode)
	}
}

func TestNativeProcessIdentitySeedsObserverReconciliation(t *testing.T) {
	t.Parallel()
	store := registry.NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	process := &registry.ProcessIdentity{PID: 42, StartIdentity: "boot:42"}
	tmux := &registry.TmuxContext{Inside: true, SessionName: "dev", PaneID: "%4"}
	activity := registry.ActivityRunning
	native, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
		Harness: registry.HarnessPi, Identity: registry.ObservationIdentity{SessionPath: "/tmp/pi-session.json"},
		Process: process, Tmux: tmux, NativeEvent: "agent_start", Activity: &activity, ObservedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.Observe(ctx, registry.Observation{
		Source: registry.ObservationSourceProcess, Evidence: registry.ObservationEvidenceProcessPresence,
		Harness: registry.HarnessPi, ProcessPresent: new(true), Process: process, ObservedAt: at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observed.ID != native.ID || observed.Presence != registry.PresenceLive || observed.Activity == nil || *observed.Activity != registry.ActivityRunning || observed.Tmux.PaneID != "%4" {
		t.Fatalf("observer did not reconcile with native process identity: native=%#v observed=%#v", native, observed)
	}
}
