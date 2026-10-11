package registry_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestNativeReportActivityRetention(t *testing.T) {
	t.Parallel()

	process := registry.ProcessIdentity{PID: 86, StartIdentity: "boot:86"}
	replacement := registry.ProcessIdentity{PID: 86, StartIdentity: "boot:87"}
	const integration = "test-agent-extension"
	for _, test := range []struct {
		name       string
		previous   registry.Activity
		reporter   string
		process    *registry.ProcessIdentity
		activity   *registry.Activity
		lifecycle  *registry.NativeLifecycle
		presence   registry.Presence
		ended      bool
		wantNative *registry.Activity
	}{
		{name: "running", previous: registry.ActivityRunning, reporter: integration, process: &process, presence: registry.PresenceLive, wantNative: new(registry.ActivityRunning)},
		{name: "idle", previous: registry.ActivityIdle, reporter: integration, process: &process, presence: registry.PresenceLive, wantNative: new(registry.ActivityIdle)},
		{name: "waiting", previous: registry.ActivityWaiting, reporter: integration, process: &process, presence: registry.PresenceLive, wantNative: new(registry.ActivityWaiting)},
		{name: "explicit idle", previous: registry.ActivityRunning, reporter: integration, process: &process, activity: new(registry.ActivityIdle), presence: registry.PresenceLive, wantNative: new(registry.ActivityIdle)},
		{name: "explicit unknown", previous: registry.ActivityRunning, reporter: integration, process: &process, activity: new(registry.ActivityUnknown), presence: registry.PresenceLive, wantNative: new(registry.ActivityUnknown)},
		{name: "different reporter", previous: registry.ActivityRunning, reporter: "other-reporter", process: &process, presence: registry.PresenceLive},
		{name: "missing reporter", previous: registry.ActivityRunning, process: &process, presence: registry.PresenceLive},
		{name: "replaced process", previous: registry.ActivityRunning, reporter: integration, process: &replacement, presence: registry.PresenceLive},
		{name: "missing process", previous: registry.ActivityRunning, reporter: integration, presence: registry.PresenceLive},
		{name: "terminal lifecycle", previous: registry.ActivityRunning, reporter: integration, process: &process, lifecycle: new(registry.NativeLifecycleEnd), presence: registry.PresenceGone},
		{name: "terminal presence", previous: registry.ActivityRunning, reporter: integration, process: &process, presence: registry.PresenceGone},
		{name: "start after process exit", previous: registry.ActivityRunning, reporter: integration, process: &process, lifecycle: new(registry.NativeLifecycleStart), presence: registry.PresenceLive, ended: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := registry.NewJournal(filepath.Join(t.TempDir(), "sessions.json"), behaviorRules{})
			at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			harnessID := registry.Harness("test-agent")
			identity := registry.ObservationIdentity{SessionID: "session"}
			_, err := store.Observe(t.Context(), registry.Observation{Harness: harnessID, At: at, Subject: identity, Evidence: &registry.Report{
				Reporter: registry.Reporter{Integration: integration}, Activity: &test.previous, Claim: new(registry.PresenceLive), Process: &process,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if test.ended {
				if _, err := store.Observe(t.Context(), registry.Observation{Harness: harnessID, At: at.Add(time.Second), Subject: identity, Evidence: &registry.Sighting{Process: process, Present: false}}); err != nil {
					t.Fatal(err)
				}
			}

			session, err := store.Observe(t.Context(), registry.Observation{Harness: harnessID, At: at.Add(2 * time.Second), Subject: identity, Evidence: &registry.Report{
				Reporter: registry.Reporter{Integration: test.reporter}, Activity: test.activity, Claim: &test.presence, Process: test.process, Lifecycle: test.lifecycle,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(test.wantNative, session.Observations.Native.Activity); diff != "" {
				t.Fatalf("native activity mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
