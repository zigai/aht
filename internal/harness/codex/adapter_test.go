package codex_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestRunningClockSurvivesScreenGapsAndLongToolSilence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state := registry.State{}
	state, _ = applyClockObservation(
		t,
		state,
		clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning),
	)

	for index, activity := range []registry.Activity{
		registry.ActivityRunning,
		registry.ActivityUnknown,
		registry.ActivityIdle,
		registry.ActivityWaiting,
		registry.ActivityRunning,
	} {
		observationAt := at.Add(time.Duration(index+1) * time.Minute)

		var session registry.Session

		state, session = applyClockObservation(
			t,
			state,
			clockReading(observationAt, process, activity),
		)
		requireClockState(t, session, registry.ActivityRunning, at)
	}

	_, session := applyClockObservation(
		t,
		state,
		clockReport(at.Add(6*time.Minute), process, "PostToolUse", registry.ActivityRunning),
	)
	requireClockState(t, session, registry.ActivityRunning, at)
}

func TestNativeTurnTransitionsUpdateClock(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state := registry.State{}
	transitions := []struct {
		event    string
		activity registry.Activity
	}{
		{"UserPromptSubmit", registry.ActivityRunning},
		{"PermissionRequest", registry.ActivityWaiting},
		{"PostToolUse", registry.ActivityRunning},
		{"Stop", registry.ActivityIdle},
		{"UserPromptSubmit", registry.ActivityRunning},
		{"Interrupt", registry.ActivityInterrupted},
		{"UserPromptSubmit", registry.ActivityRunning},
	}

	for index, transition := range transitions {
		observationAt := at.Add(time.Duration(index) * time.Minute)

		var session registry.Session

		state, session = applyClockObservation(
			t,
			state,
			clockReport(observationAt, process, transition.event, transition.activity),
		)
		requireClockState(t, session, transition.activity, observationAt)
		state, session = applyClockObservation(
			t,
			state,
			clockReading(observationAt.Add(45*time.Second), process, registry.ActivityUnknown),
		)
		requireClockState(t, session, transition.activity, observationAt)
	}
}

func TestAutomaticCompactionKeepsRunningClock(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state, _ := applyClockObservation(
		t,
		registry.State{},
		clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning),
	)
	state, _ = applyClockObservation(
		t,
		state,
		clockReport(at.Add(time.Minute), process, "PreCompact", registry.ActivityRunning),
	)
	observation := clockReport(
		at.Add(2*time.Minute),
		process,
		"SessionStart",
		registry.ActivityRunning,
	)
	observation.Report().Activity = nil
	observation.Report().Lifecycle = new(registry.NativeLifecycleStart)
	state, session := applyClockObservation(t, state, observation)
	requireClockState(t, session, registry.ActivityRunning, at)
	_, session = applyClockObservation(
		t,
		state,
		clockReading(at.Add(3*time.Minute), process, registry.ActivityUnknown),
	)
	requireClockState(t, session, registry.ActivityRunning, at)
}

func TestProcessReplacementReturnsToScreenFallback(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}
	state, _ := applyClockObservation(
		t,
		registry.State{},
		clockReport(at, process, "UserPromptSubmit", registry.ActivityRunning),
	)
	process.StartIdentity = "boot:124"
	state, _ = applyClockObservation(t, state, registry.Observation{
		Harness: registry.Harness("codex"), At: at.Add(time.Minute),
		Subject:  registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Sighting{Process: process, Present: true},
	})
	_, session := applyClockObservation(
		t,
		state,
		clockReading(at.Add(2*time.Minute), process, registry.ActivityIdle),
	)
	requireClockState(t, session, registry.ActivityIdle, at.Add(2*time.Minute))

	if session.Decision() == nil || session.Decision().Authority != registry.AuthorityScreen {
		t.Fatalf("decision = %v, want screen fallback", session.Decision())
	}
}

func TestScreenFallbackWithoutMatchingNativeEvidence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "boot:123", Executable: "codex"}

	for _, reporter := range []string{"", "unrelated-hook"} {
		t.Run(reporter, func(t *testing.T) {
			t.Parallel()

			state := registry.State{}

			if reporter != "" {
				observation := clockReport(
					at,
					process,
					"UserPromptSubmit",
					registry.ActivityRunning,
				)
				observation.Report().Reporter.Integration = reporter
				state, _ = applyClockObservation(t, state, observation)
			}

			_, session := applyClockObservation(
				t,
				state,
				clockReading(at.Add(time.Minute), process, registry.ActivityWaiting),
			)
			requireClockState(t, session, registry.ActivityWaiting, at.Add(time.Minute))

			if session.Decision() == nil ||
				session.Decision().Authority != registry.AuthorityScreen {
				t.Fatalf("decision = %v, want screen fallback", session.Decision())
			}
		})
	}
}

func clockReport(
	at time.Time,
	process registry.ProcessIdentity,
	event string,
	activity registry.Activity,
) registry.Observation {
	return registry.Observation{
		Harness: registry.Harness("codex"), At: at,
		Subject: registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Report{
			Reporter: registry.Reporter{Integration: "codex-hook"},
			Event:    event,
			Claim:    new(registry.PresenceLive),
			Activity: new(activity),
			Process:  &process,
		},
	}
}

func clockReading(
	at time.Time,
	process registry.ProcessIdentity,
	activity registry.Activity,
) registry.Observation {
	return registry.Observation{
		Harness: registry.Harness("codex"), At: at,
		Subject: registry.ObservationIdentity{SessionID: "clock-session"},
		Evidence: &registry.Reading{
			Activity:  activity,
			Authority: registry.AuthorityScreen,
			Reason:    "manifest_rule",
			RuleID:    "screen-probe",
			Process:   process,
		},
	}
}

func applyClockObservation(
	t *testing.T,
	state registry.State,
	observation registry.Observation,
) (registry.State, registry.Session) {
	t.Helper()

	state, _, err := registry.NewReducer(catalog.Rules{}).
		Apply(state, []registry.Observation{observation}, observation.At)
	if err != nil {
		t.Fatal(err)
	}

	if len(state.Sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(state.Sessions))
	}

	for _, session := range state.Sessions {
		return state, session
	}

	return state, registry.Session{}
}

func requireClockState(
	t *testing.T,
	session registry.Session,
	activity registry.Activity,
	since time.Time,
) {
	t.Helper()

	if session.Activity() == nil || *session.Activity() != activity {
		t.Fatalf("activity = %v, want %s", session.Activity(), activity)
	}

	if !session.ActivityChangedAt.Equal(since) {
		t.Fatalf("activity clock = %s, want %s", session.ActivityChangedAt, since)
	}
}

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name       string
		harness    registry.Harness
		payload    string
		wantID     string
		wantPath   string
		wantCWD    string
		wantRoot   string
		wantEvent  string
		wantAttr   string
		wantAttrKV string
	}{
		{
			name:    "codex",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":"/tmp/` +
				`codex.jsonl","cwd":"/tmp","hook_event_name":"UserPromptSubmi` +
				`t","model":"gpt-5-codex"}`,
			wantID:     "codex-session",
			wantPath:   "/tmp/codex.jsonl",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "UserPromptSubmit",
			wantAttr:   "codex_model",
			wantAttrKV: "gpt-5-codex",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := catalog.PayloadDefaults(test.harness, json.RawMessage(test.payload))
			if err != nil {
				t.Fatalf("unexpected error from PayloadDefaults: %v", err)
			}

			if got.SessionID != test.wantID ||
				got.SessionPath != test.wantPath ||
				got.CWD != test.wantCWD ||
				got.ProjectRoot != test.wantRoot ||
				got.Event != test.wantEvent {
				t.Fatalf("unexpected defaults: %#v", got)
			}

			if got.Attributes[test.wantAttr] != test.wantAttrKV {
				t.Fatalf(
					"expected attribute %s=%q, got %#v",
					test.wantAttr,
					test.wantAttrKV,
					got.Attributes,
				)
			}
		})
	}
}

func TestPayloadCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		harness registry.Harness
		payload string
		want    bool
	}{
		{
			name:    "codex accepts native hook payload",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":"/home/user/` +
				`.codex/sessions/2026/06/18/rollout.jsonl","cwd":"/repo",` +
				`"hook_event_name":"Stop","model":"gpt-5-codex"}`,
			want: true,
		},
		{
			name:    "codex accepts null transcript path",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":null,` +
				`"cwd":"/repo","hook_event_name":"Stop",` +
				`"model":"gpt-5-codex"}`,
			want: true,
		},
		{
			name:    "codex accepts session end payload without model",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"codex-session","transcript_path":null,` +
				`"cwd":"/repo","hook_event_name":"SessionEnd",` +
				`"reason":"other"}`,
			want: true,
		},
		{
			name:    "codex accepts configurable transcript path",
			harness: registry.Harness("codex"),
			payload: `{"session_id":"claude-session","transcript_path":"/home/` +
				`user/.claude/projects/-repo/claude-session.jsonl","cwd":"/` +
				`repo","hook_event_name":"SessionStart",` +
				`"model":"claude-sonnet-4-6"}`,
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := catalog.PayloadCompatible(test.harness, json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("codex"), variable: "CODEX_HOME"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, catalog.All()...)

				if !knownHome {
					t.Setenv("HOME", "")
					t.Setenv("USERPROFILE", "")
				}

				override := t.TempDir()

				value := override
				if test.value != nil {
					value = test.value(override)
				}

				t.Setenv(test.variable, value)
				locations, _ := catalog.LocationsFor(test.harness, "")

				config := testharness.LocationPaths(locations, harness.LocationKindConfig)
				if len(config) == 0 || slices.ContainsFunc(config, func(path string) bool {
					return !strings.HasPrefix(path, override+string(filepath.Separator)) &&
						!strings.HasPrefix(path, "/etc/")
				}) {
					t.Errorf("config locations with %s=%s = %v", test.variable, value, config)
				}
			})
		}
	}
}

func TestSessionTitlesIgnoreWorkingDirectoryWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		plant   func(t *testing.T, workDir string)
	}{
		{registry.Harness("codex"), func(t *testing.T, workDir string) {
			t.Helper()
			testharness.PlantFile(
				t, workDir, `{"id":"abc","thread_name":"Decoy"}`+"\n", ".codex", "session_index.jsonl",
			)
		}},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			workDir := testharness.WithoutHome(t, catalog.All()...)
			test.plant(t, workDir)

			adapter, ok := catalog.Find(test.harness)
			if !ok {
				t.Fatalf("adapter %s not found", test.harness)
			}

			reader, ok := adapter.(harness.TitleReader)
			if !ok {
				t.Fatalf("adapter %s has no title reader", test.harness)
			}

			titles, err := reader.SessionTitles(
				t.Context(),
				[]registry.ObservationIdentity{{SessionID: "abc"}},
			)
			if err != nil || len(titles) != 1 || titles[0] != "" {
				t.Fatalf("SessionTitles without a home = %q, %v, want one empty title", titles, err)
			}
		})
	}
}

func TestNativeDetailsUseExplicitPromptSignals(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		harness    registry.Harness
		event      string
		activity   registry.Activity
		attributes map[string]string
		want       *registry.ActivityDetail
	}{
		{"codex", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
	} {
		t.Run(string(test.harness)+"/"+test.event+string(test.activity), func(t *testing.T) {
			report := &registry.Report{
				Event:      test.event,
				Activity:   &test.activity,
				Attributes: test.attributes,
			}

			observation := catalog.PrepareObservation(
				registry.Observation{Harness: test.harness, Evidence: report},
			)
			if !reflect.DeepEqual(observation.Report().Detail, test.want) {
				t.Fatalf("detail = %v, want %v", observation.Report().Detail, test.want)
			}
		})
	}
}
