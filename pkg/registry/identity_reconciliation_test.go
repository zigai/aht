package registry_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestIdentityReconciliationPrefersSessionPath(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	at := time.Now().UTC().Add(-time.Minute)

	first, err := store.Observe(
		context.Background(),
		registry.Observation{
			Harness:  registry.Harness("catalog"),
			At:       at,
			Subject:  registry.ObservationIdentity{SessionPath: "/tmp/session.json"},
			Evidence: &registry.Report{Event: "test"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	second, err := store.Observe(
		context.Background(),
		registry.Observation{
			Harness: registry.Harness("catalog"),
			At:      at.Add(time.Second),
			Subject: registry.ObservationIdentity{SessionPath: "/tmp/session.json"},
			Evidence: &registry.Sighting{
				Process: registry.ProcessIdentity{PID: 41, StartIdentity: "boot:41"},
				Present: true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if first.ID != second.ID || second.Presence() != registry.PresenceLive {
		t.Fatalf("observations did not reconcile: first=%#v second=%#v", first, second)
	}
}

func TestNativeProcessIdentityReconcilesWithLiveTmuxSession(t *testing.T) {
	t.Parallel()
	store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Minute)
	path := "/tmp/secondary-session.json"
	process := &registry.ProcessIdentity{
		PID:            42,
		PPID:           10,
		ProcessGroupID: 42,
		StartIdentity:  "boot:42",
		Executable:     "/usr/bin/node",
		CWD:            "/work",
		TTY:            "/dev/pts/4",
	}
	idle := registry.ActivityIdle

	identityOnly, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("secondary"),
			At:       at,
			Subject:  registry.ObservationIdentity{SessionPath: path},
			Evidence: &registry.Report{Event: "session_start", Activity: &idle},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	live, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("secondary"),
			At:       at.Add(time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Sighting{Process: *process, Present: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	tmux := &registry.Location{
		Kind:        registry.MultiplexerTmux,
		SessionName: "main",
		PaneID:      "%81",
		PaneTTY:     "/dev/pts/4",
	}
	if _, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("secondary"),
			At:       at.Add(2 * time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Placement{Process: *process, Location: *tmux},
		},
	); err != nil {
		t.Fatal(err)
	}

	running := registry.ActivityRunning

	reconciled, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("secondary"),
			At:       at.Add(3 * time.Second),
			Subject:  registry.ObservationIdentity{SessionPath: path},
			Evidence: &registry.Report{Event: "agent_start", Activity: &running, Process: process},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if reconciled.ID != live.ID || reconciled.ID == identityOnly.ID {
		t.Fatalf(
			"native report reconciled to %q, want live process record %q (identity-only %q)",
			reconciled.ID,
			live.ID,
			identityOnly.ID,
		)
	}

	if reconciled.Presence() != registry.PresenceLive || reconciled.Activity() == nil ||
		*reconciled.Activity() != registry.ActivityRunning ||
		reconciled.SessionPath != path ||
		reconciled.Location.PaneID != "%81" {
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

	previous, err := store.Observe(
		ctx,
		registry.Observation{
			Harness: registry.Harness("separate"),
			At:      at,
			Subject: registry.ObservationIdentity{SessionID: "separate-session"},
			Evidence: &registry.Report{
				Event:    "session_start",
				Claim:    &live,
				Activity: &idle,
				Process:  process,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	present := true

	replacement, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("alternate"),
			At:       at.Add(time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Sighting{Process: *process, Present: present},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if replacement.Harness != registry.Harness("alternate") ||
		replacement.Presence() != registry.PresenceLive {
		t.Fatalf("replacement process session is not live: %#v", replacement)
	}

	previous, err = store.Get(ctx, previous.ID)
	if err != nil {
		t.Fatal(err)
	}

	if previous.Harness != registry.Harness("separate") ||
		previous.Presence() != registry.PresenceGone ||
		previous.Activity() != nil {
		t.Fatalf("previous session was not retired: %#v", previous)
	}

	sessions, err := store.List(ctx, registry.Filter{Presence: registry.PresenceLive})
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].ID != replacement.ID {
		t.Fatalf("live sessions = %#v, want only replacement %q", sessions, replacement.ID)
	}
}

func TestMultiplexerObservationRetiresDifferentHarnessWithoutProcessIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		prior, next registry.Location
		replace     bool
	}{
		{
			name: "tmux server same",
			prior: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			replace: true,
		},
		{
			name: "tmux server different",
			prior: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/other/socket",
			},
			replace: false,
		},
		{
			name: "zellij session same",
			prior: registry.Location{
				Kind:        registry.MultiplexerZellij,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerZellij,
				PaneID:      "7",
				SessionName: "work",
			},
			replace: true,
		},
		{
			name: "zellij session different",
			prior: registry.Location{
				Kind:        registry.MultiplexerZellij,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerZellij,
				PaneID:      "7",
				SessionName: "other",
			},
			replace: false,
		},
		{
			name: "herdr server same",
			prior: registry.Location{
				Kind:     registry.MultiplexerHerdr,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerHerdr,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			replace: true,
		},
		{
			name: "herdr server different",
			prior: registry.Location{
				Kind:     registry.MultiplexerHerdr,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerHerdr,
				PaneID:   "7",
				ServerID: "/other/socket",
			},
			replace: false,
		},
		{
			name: "herdr session same",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
			},
			replace: true,
		},
		{
			name: "herdr session different",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "other",
			},
			replace: false,
		},
		{
			name: "herdr listed pane and environment",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
				ServerID:    "/sample/socket",
			},
			replace: true,
		},
		{
			name: "tmux normalized server",
			prior: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/sample/dir/../socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "7",
				ServerID: "/sample/socket",
			},
			replace: true,
		},

		{
			name: "different pane",
			prior: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "%7",
				ServerID: "/sample/socket",
			},
			next: registry.Location{
				Kind:     registry.MultiplexerTmux,
				PaneID:   "%8",
				ServerID: "/sample/socket",
			},
		},
		{
			name: "different kind",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerZellij,
				PaneID:      "7",
				SessionName: "work",
			},
		},
		{
			name:  "missing session",
			prior: registry.Location{Kind: registry.MultiplexerZellij, PaneID: "7"},
			next:  registry.Location{Kind: registry.MultiplexerZellij, PaneID: "7"},
		},
		{
			name: "herdr same server different session",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				ServerID:    "/sample/socket",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				ServerID:    "/sample/socket",
				SessionName: "other",
			},
			replace: true,
		},
		{
			name: "herdr same session different server",
			prior: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				ServerID:    "/sample/socket",
				SessionName: "work",
			},
			next: registry.Location{
				Kind:        registry.MultiplexerHerdr,
				PaneID:      "7",
				ServerID:    "/other/socket",
				SessionName: "work",
			},
			replace: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := registry.NewJournal(
				filepath.Join(t.TempDir(), "sessions.json"),
				behaviorRules{},
			)
			ctx := context.Background()
			at := time.Now().UTC().Add(-time.Minute)
			location := &test.prior
			live := registry.PresenceLive
			idle := registry.ActivityIdle

			previous, err := store.Observe(
				ctx,
				registry.Observation{
					Harness: registry.Harness("separate"),
					At:      at,
					Subject: registry.ObservationIdentity{SessionID: "separate-session"},
					Evidence: &registry.Report{
						Event:    "session_start",
						Claim:    &live,
						Activity: &idle,
						Location: location,
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if previous.Process != nil {
				t.Fatalf("fixture unexpectedly has process identity: %#v", previous)
			}

			process := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

			present := true
			if _, err = store.Observe(
				ctx,
				registry.Observation{
					Harness:  registry.Harness("alternate"),
					At:       at.Add(time.Second),
					Subject:  registry.ObservationIdentity{},
					Evidence: &registry.Sighting{Process: *process, Present: present},
				},
			); err != nil {
				t.Fatal(err)
			}

			replacement, err := store.Observe(
				ctx,
				registry.Observation{
					Harness:  registry.Harness("alternate"),
					At:       at.Add(2 * time.Second),
					Subject:  registry.ObservationIdentity{},
					Evidence: &registry.Placement{Process: *process, Location: test.next},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			previous, err = store.Get(ctx, previous.ID)
			if err != nil {
				t.Fatal(err)
			}

			want := registry.PresenceLive
			if test.replace {
				want = registry.PresenceGone
			}

			if previous.Presence() != want {
				t.Fatalf("previous presence = %s, want %s", previous.Presence(), want)
			}

			if test.replace &&
				(previous.Activity() != nil || !previous.ActivityChangedAt.Equal(at.Add(2*time.Second))) {
				t.Fatalf("processless session was not retired at replacement time: %#v", previous)
			}

			if replacement.Harness != registry.Harness("alternate") ||
				replacement.Presence() != registry.PresenceLive ||
				replacement.Location.PaneID != test.next.PaneID {
				t.Fatalf("replacement is not live on the pane: %#v", replacement)
			}
		})
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
	existingProcess := &registry.ProcessIdentity{PID: 84, StartIdentity: "boot:84"}
	ompProcess := &registry.ProcessIdentity{PID: 85, StartIdentity: "boot:85"}

	existing, err := store.Observe(
		ctx,
		registry.Observation{
			Harness: registry.Harness("separate"),
			At:      at,
			Subject: registry.ObservationIdentity{SessionID: "separate-session"},
			Evidence: &registry.Report{
				Event:    "session_start",
				Claim:    &live,
				Process:  existingProcess,
				Location: location,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	present := true
	if _, err = store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("separate"),
			At:       at.Add(2 * time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Sighting{Process: *existingProcess, Present: present},
		},
	); err != nil {
		t.Fatal(err)
	}

	if _, err = store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("alternate"),
			At:       at.Add(2 * time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Sighting{Process: *ompProcess, Present: present},
		},
	); err != nil {
		t.Fatal(err)
	}

	if _, err = store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("alternate"),
			At:       at.Add(2 * time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Placement{Process: *ompProcess, Location: *location},
		},
	); err != nil {
		t.Fatal(err)
	}

	existing, err = store.Get(ctx, existing.ID)
	if err != nil {
		t.Fatal(err)
	}

	if existing.Presence() != registry.PresenceLive || existing.Process == nil ||
		!existing.Process.Equal(*existingProcess) {
		t.Fatalf("distinct live process was retired: %#v", existing)
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

	native, err := store.Observe(
		ctx,
		registry.Observation{
			Harness: registry.Harness("secondary"),
			At:      at,
			Subject: registry.ObservationIdentity{SessionPath: "/tmp/secondary-session.json"},
			Evidence: &registry.Report{
				Event:    "agent_start",
				Activity: &activity,
				Process:  process,
				Location: tmux,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	observed, err := store.Observe(
		ctx,
		registry.Observation{
			Harness:  registry.Harness("secondary"),
			At:       at.Add(time.Second),
			Subject:  registry.ObservationIdentity{},
			Evidence: &registry.Sighting{Process: *process, Present: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if observed.ID != native.ID || observed.Presence() != registry.PresenceLive ||
		observed.Activity() == nil ||
		*observed.Activity() != registry.ActivityRunning ||
		observed.Location.PaneID != "%4" {
		t.Fatalf(
			"observer did not reconcile with native process identity: native=%#v observed=%#v",
			native,
			observed,
		)
	}
}

func TestResumedProcessIsNotMarkedGoneByOlderTombstone(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	state := registry.State{Sessions: map[string]registry.Session{}}

	// A session known only by its native id, later verified gone.
	state = applyObservations(
		t,
		state,
		base,
		nativeReport("resumed", base, nil, registry.ActivityIdle),
	)
	state = applyObservations(t, state, base.Add(time.Second), registry.Observation{
		Harness:  registry.Harness("primary"),
		At:       base.Add(time.Second),
		Subject:  registry.ObservationIdentity{SessionID: "resumed"},
		Evidence: &registry.Verification{Presence: registry.PresenceGone, Reason: "no_process"},
	})

	process := reducerProcess(200)
	placedAt := base.Add(time.Minute)
	state = applyObservations(t, state, placedAt, registry.Observation{
		Harness: registry.Harness("primary"),
		At:      placedAt,
		Evidence: &registry.Placement{
			Process: process,
			Location: registry.Location{
				Kind:      registry.MultiplexerTmux,
				ServerID:  "srv",
				SessionID: "$1",
				PaneID:    "%1",
			},
		},
	})
	state = applyObservations(t, state, placedAt.Add(time.Second),
		nativeReport("resumed", placedAt.Add(time.Second), &process, registry.ActivityRunning))

	session := sessionWithProcess(t, state, process.PID)
	if session.SessionID != "resumed" ||
		registry.ActivityValue(session.Activity()) != registry.ActivityRunning {
		t.Fatalf("resumed session = id %q activity %v, want id resumed activity running",
			session.SessionID, registry.ActivityValue(session.Activity()))
	}
}

func TestListingMatchedOnlyByPIDKeepsSessionID(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	process := reducerProcess(123)
	state := registry.State{Sessions: map[string]registry.Session{}}

	state = applyObservations(
		t,
		state,
		base,
		nativeReport("first", base, &process, registry.ActivityRunning),
	)
	state = applyObservations(t, state, base.Add(time.Second), registry.Observation{
		Harness:  registry.Harness("primary"),
		At:       base.Add(time.Second),
		Subject:  registry.ObservationIdentity{SessionID: "other"},
		Evidence: &registry.Listing{ProcessPID: process.PID, Current: false},
	})

	if session := sessionWithProcess(t, state, process.PID); session.SessionID != "first" {
		t.Fatalf(
			"session id after a non-current listing for another id = %q, want first",
			session.SessionID,
		)
	}
}
