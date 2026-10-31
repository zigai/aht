package observer

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/internal/registrycheck"
	"github.com/zigai/aht/v2/pkg/mux"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestObserverReconcilesUnknownNativeSession(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		panePID   int
		processes []processinfo.Process
		want      registry.Presence
	}{
		{name: "missing pane", panePID: 1392, want: registry.PresenceGone},
		{
			name:    "existing pane",
			panePID: 1392,
			processes: []processinfo.Process{
				{
					PID:           1392,
					StartIdentity: "boot:1392",
					Executable:    "/bin/zsh",
				},
			},
			want: registry.PresenceUnknown,
		},
		{name: "no process evidence", want: registry.PresenceUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sessions.json")
			at := time.Now().UTC().Add(-time.Hour)
			store := registry.NewJournal(path, catalog.Rules{})
			activity := registry.ActivityIdle

			session, err := store.Observe(
				t.Context(),
				registry.Observation{
					Harness: registry.Harness("opencode"),
					At:      at,
					Subject: registry.ObservationIdentity{SessionID: "native-idle"},
					Evidence: &registry.Report{
						Event:    "session.idle",
						Activity: &activity,
						Location: &registry.Location{
							Kind:    registry.MultiplexerTmux,
							PaneID:  "%9",
							PanePID: test.panePID,
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			watcher := New(Options{
				StorePath:   path,
				HealthPath:  path + ".health",
				Now:         func() time.Time { return at.Add(time.Minute) },
				ProcessList: func(context.Context) ([]processinfo.Process, error) { return test.processes, nil },
				PaneList:    func(context.Context) ([]mux.Pane, error) { return nil, nil },
				CatalogList: func(context.Context) ([]CatalogEntry, error) { return nil, nil },
			})
			if _, err := watcher.RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}

			got, err := store.Get(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}

			if got.Presence() != test.want {
				t.Fatalf("presence = %q, want %q", got.Presence(), test.want)
			}

			if test.want == registry.PresenceGone && got.Activity() != nil {
				t.Fatalf("retired session retains activity: %+v", got)
			}
		})
	}
}

func TestObserverReconcilesUnverifiedLiveSession(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		located bool
		process *registry.ProcessIdentity
		check   func(context.Context, registry.Location) (registry.Presence, error)
		want    registry.Presence
	}{
		{name: "missing saved server", located: true, want: registry.PresenceGone},
		{name: "no owner evidence", want: registry.PresenceUnknown},
		{
			name:    "existing pane without owner",
			located: true,
			check: func(context.Context, registry.Location) (registry.Presence, error) {
				return registry.PresenceLive, nil
			},
			want: registry.PresenceUnknown,
		},
		{
			name:    "failed location inspection",
			located: true,
			check: func(
				context.Context,
				registry.Location,
			) (registry.Presence, error) {
				return registry.PresenceUnknown, errSimulationPane
			},
			want: registry.PresenceUnknown,
		},
		{
			name:    "running process overrides missing server",
			located: true,
			process: &registry.ProcessIdentity{
				PID:           1392,
				StartIdentity: "boot:1392",
				Executable:    "/bin/opencode",
			},
			want: registry.PresenceLive,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sessions.json")
			at := time.Now().UTC().Add(-time.Hour)
			store := registry.NewJournal(path, catalog.Rules{})
			idle, live := registry.ActivityIdle, registry.PresenceLive

			var location *registry.Location
			if test.located {
				location = &registry.Location{
					Kind:     registry.MultiplexerTmux,
					ServerID: filepath.Join(t.TempDir(), "missing.sock"),
					PaneID:   "%9",
				}
			}

			session, err := store.Observe(
				t.Context(),
				registry.Observation{
					Harness: registry.Harness("opencode"),
					At:      at,
					Subject: registry.ObservationIdentity{SessionID: "native-idle"},
					Evidence: &registry.Report{
						Reporter: registry.Reporter{
							Integration: "opencode-plugin",
							Sequence:    new(uint64(1)),
						},
						Event:    "session.created",
						Claim:    &live,
						Activity: &idle,
						Location: location,
						Process:  test.process,
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			var processes []processinfo.Process
			if test.process != nil {
				processes = []processinfo.Process{
					{
						PID:           test.process.PID,
						StartIdentity: test.process.StartIdentity,
						Executable:    test.process.Executable,
					},
				}
			}

			watcher := New(Options{
				StorePath:               path,
				Now:                     func() time.Time { return at.Add(time.Minute) },
				DisableScreenInspection: true,
				LocationPresence:        test.check,
				ProcessList:             func(context.Context) ([]processinfo.Process, error) { return processes, nil },
				PaneList:                func(context.Context) ([]mux.Pane, error) { return nil, nil },
				CatalogList:             func(context.Context) ([]CatalogEntry, error) { return nil, nil },
			})

			result, runErr := watcher.RunOnce(t.Context())
			if runErr != nil {
				t.Fatal(runErr)
			}

			if test.name == "failed location inspection" &&
				(!result.Degraded || result.Error == "") {
				t.Fatalf("failed location inspection result = %+v", result)
			}

			got, err := store.Get(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}

			if got.Presence() != test.want {
				t.Fatalf("presence = %q, want %q", got.Presence(), test.want)
			}

			if err := registrycheck.Sessions([]registry.Session{got}); err != nil {
				t.Fatal(err)
			}

			if err := registrycheck.Verification(session, got); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got.Observations.Native, session.Observations.Native) {
				t.Fatal("reconciliation changes the native report")
			}
		})
	}
}
