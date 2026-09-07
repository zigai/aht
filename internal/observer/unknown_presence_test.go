package observer

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/internal/processinfo"
	"github.com/zigai/aht/pkg/mux"
	"github.com/zigai/aht/pkg/registry"
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
		{name: "existing pane", panePID: 1392, processes: []processinfo.Process{{PID: 1392, StartIdentity: "boot:1392", Executable: "/bin/zsh"}}, want: registry.PresenceUnknown},
		{name: "no process evidence", want: registry.PresenceUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sessions.json")
			at := time.Now().UTC().Add(-time.Hour)
			store := registry.NewFileStore(path)
			activity := registry.ActivityIdle
			session, err := store.Observe(t.Context(), registry.Observation{
				Source: registry.ObservationSourceNative, Evidence: registry.ObservationEvidenceNativeEvent,
				Harness: registry.HarnessOpenCode, Identity: registry.ObservationIdentity{SessionID: "native-idle"},
				NativeEvent: "session.idle", Activity: &activity,
				Tmux: &registry.TmuxContext{Inside: true, PaneID: "%9", PanePID: test.panePID}, ObservedAt: at,
			})
			if err != nil {
				t.Fatal(err)
			}
			watcher := New(Options{
				StorePath: path, HealthPath: path + ".health", Now: func() time.Time { return at.Add(time.Minute) },
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
			if got.Presence != test.want {
				t.Fatalf("presence = %q, want %q", got.Presence, test.want)
			}
			if test.want == registry.PresenceGone && got.Activity != nil {
				t.Fatalf("retired session retains activity: %+v", got)
			}
		})
	}
}
