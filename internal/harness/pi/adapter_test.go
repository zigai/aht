package pi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestShutdownSurvivesEqualObservationTimestamps(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated extension")
	}

	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	script := "#!" + node + "\nrequire('node:fs').appendFileSync(process.env.AHT_CAPTURE, " +
		"JSON.stringify(process.argv.slice(2))+'\\n');\n"

	if err := os.WriteFile(reporter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	module := harness.RenderScriptTemplate(piExtensionTemplate, reporter, New().Definition())
	if err := os.WriteFile(filepath.Join(dir, "extension.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}

	driver := `import extension from "./extension.ts";
const handlers = new Map();
extension({ on: (name, handler) => handlers.set(name, handler) });
const ctx = { cwd: "/work", mode: "rpc", sessionManager: { getSessionId: () => "native-session" } };
for (const type of ["session_start", "agent_end", "agent_settled", "session_shutdown"]) {
  await handlers.get(type)({ type }, ctx);
}
process.exit(0);
`

	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)

	command.Env = append(os.Environ(), "AHT_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("extension shutdown: %v\n%s", err, output)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}

	store := registry.NewJournal(filepath.Join(dir, "state.json"), piTestRules{})
	// Deliver the generated reports into the real consumer with an equal clock
	// reading, reproducing callbacks occurring within one Date millisecond.
	at := time.Now().UTC()

	var session registry.Session

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}

		observation := registry.Observation{
			Harness: registry.Harness("pi"),
			At:      at,
			Subject: registry.ObservationIdentity{SessionID: "native-session"},
			Evidence: &registry.Report{
				Reporter: registry.Reporter{Integration: piIntegrationSourceID},
			},
		}

		for index := 0; index+1 < len(args); index++ {
			switch args[index] {
			case "--event":
				observation.Report().Event = args[index+1]
			case "--activity":
				activity := registry.Activity(args[index+1])
				observation.SetActivity(&activity)
			case "--presence":
				presence := registry.Presence(args[index+1])
				observation.Report().Claim = &presence
			case "--sequence":
				sequence, err := strconv.ParseUint(args[index+1], 10, 64)
				if err != nil {
					t.Fatal(err)
				}

				observation.Report().Reporter.Sequence = &sequence
			}
		}

		session, err = store.Observe(t.Context(), observation)
		if err != nil {
			t.Fatalf("native %s was lost: %v", observation.Report().Event, err)
		}
	}

	if session.Presence() != registry.PresenceGone || session.Observations.Native == nil ||
		session.Observations.Native.Event != "session_shutdown" {
		t.Fatalf("shutdown was not retained as native terminal evidence: %#v", session)
	}
}

type piTestRules struct{}

func (piTestRules) SamePane(a registry.Location, b registry.Location) bool { return a == b }

func (piTestRules) Known(id registry.Harness) bool { return id == New().Definition().ID }
func (piTestRules) Policy(registry.Harness) registry.Policy {
	return registry.Policy{
		ExclusiveProcess: true,
		Authority:        registry.AuthorityHook,
		ScreenFallback:   true,
		Reporter:         piIntegrationSourceID,
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("pi"), variable: "PI_CODING_AGENT_DIR"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, New())

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

				locations := New().Locations("")

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

func TestActivityDetail(t *testing.T) {
	t.Parallel()

	adapter := New()

	tests := []struct {
		name       string
		event      string
		activity   registry.Activity
		attributes map[string]string
		want       *registry.ActivityDetail
	}{
		{
			name:       "confirm prompt maps to permission",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "confirm"},
			want:       new(registry.ActivityDetailPermission),
		},
		{
			name:       "select prompt maps to question",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "select"},
			want:       new(registry.ActivityDetailQuestion),
		},
		{
			name:       "input prompt maps to question",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "input"},
			want:       new(registry.ActivityDetailQuestion),
		},
		{
			name:       "editor prompt maps to question",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "editor"},
			want:       new(registry.ActivityDetailQuestion),
		},
		{
			name:       "custom prompt leaves detail unset",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "custom"},
			want:       nil,
		},
		{
			name:       "missing prompt kind leaves detail unset",
			event:      "ui_prompt_start",
			activity:   registry.ActivityWaiting,
			attributes: nil,
			want:       nil,
		},
		{
			name:       "non-waiting activity ignores prompt kind",
			event:      "ui_prompt_start",
			activity:   registry.ActivityIdle,
			attributes: map[string]string{"pi_prompt_kind": "confirm"},
			want:       nil,
		},
		{
			name:       "other event ignores prompt kind",
			event:      "ui_prompt_end",
			activity:   registry.ActivityWaiting,
			attributes: map[string]string{"pi_prompt_kind": "confirm"},
			want:       nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := adapter.ActivityDetail(test.event, test.activity, test.attributes)
			if test.want == nil {
				if got != nil {
					t.Fatalf("ActivityDetail() = %q, want nil", *got)
				}

				return
			}

			if got == nil || *got != *test.want {
				t.Fatalf("ActivityDetail() = %v, want %q", got, *test.want)
			}
		})
	}
}

func TestUIPromptEndRestoresRestingActivity(t *testing.T) {
	t.Parallel()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated extension")
	}

	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	script := "#!" + node + "\nrequire('node:fs').appendFileSync(process.env.AHT_CAPTURE, " +
		"JSON.stringify(process.argv.slice(2))+'\\n');\n"

	if err := os.WriteFile(reporter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	module := harness.RenderScriptTemplate(piExtensionTemplate, reporter, New().Definition())
	if err := os.WriteFile(filepath.Join(dir, "extension.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}

	driver := `import extension from "./extension.ts";
const handlers = new Map();
extension({ on: (name, handler) => handlers.set(name, handler) });
let idle = true;
const ctx = {
  cwd: "/work",
  mode: "tui",
  isIdle: () => idle,
  sessionManager: { getSessionId: () => "native-session" },
};
const fire = (event) => handlers.get(event.type)(event, ctx);

fire({ type: "session_start", reason: "startup" });
idle = false;
fire({ type: "agent_start" });
fire({ type: "agent_end", messages: [{ role: "assistant", stopReason: "error" }] });
idle = true;
fire({ type: "agent_settled", aborted: false });
fire({ type: "ui_prompt_start", kind: "confirm" });
fire({ type: "ui_prompt_end", kind: "confirm" });

idle = false;
fire({ type: "agent_start" });
fire({ type: "agent_end", messages: [{ role: "assistant", stopReason: "aborted" }] });
idle = true;
fire({ type: "agent_settled", aborted: true });
fire({ type: "ui_prompt_start", kind: "select" });
fire({ type: "ui_prompt_end", kind: "select" });

fire({ type: "session_tree" });
fire({ type: "ui_prompt_start", kind: "input" });
fire({ type: "ui_prompt_end", kind: "input" });

idle = false;
fire({ type: "before_agent_start" });
fire({ type: "ui_prompt_start", kind: "editor" });
fire({ type: "ui_prompt_end", kind: "editor" });

await fire({ type: "session_shutdown" });
process.exit(0);
`

	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)

	command.Env = append(os.Environ(), "AHT_CAPTURE="+capture)

	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("extension ui_prompt flow: %v\n%s", err, output)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}

	type capturedReport struct {
		event      string
		activity   string
		presence   string
		promptKind string
	}

	var got []capturedReport

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}

		var report capturedReport

		for index := 0; index+1 < len(args); index++ {
			switch args[index] {
			case "--event":
				report.event = args[index+1]
			case "--activity":
				report.activity = args[index+1]
			case "--presence":
				report.presence = args[index+1]
			case "--attribute":
				if kind, ok := strings.CutPrefix(args[index+1], "pi_prompt_kind="); ok {
					report.promptKind = kind
				}
			}
		}

		got = append(got, report)
	}

	want := []capturedReport{
		{event: "session_start", activity: "idle"},
		{event: "agent_start", activity: "running"},
		{event: "agent_settled", activity: "failed"},
		{event: "ui_prompt_start", activity: "waiting", promptKind: "confirm"},
		{event: "ui_prompt_end", activity: "failed", promptKind: "confirm"},
		{event: "agent_start", activity: "running"},
		{event: "agent_settled", activity: "interrupted"},
		{event: "ui_prompt_start", activity: "waiting", promptKind: "select"},
		{event: "ui_prompt_end", activity: "interrupted", promptKind: "select"},
		{event: "session_tree", activity: "idle"},
		{event: "ui_prompt_start", activity: "waiting", promptKind: "input"},
		{event: "ui_prompt_end", activity: "idle", promptKind: "input"},
		{event: "before_agent_start", activity: "running"},
		{event: "ui_prompt_start", activity: "waiting", promptKind: "editor"},
		{event: "ui_prompt_end", activity: "running", promptKind: "editor"},
		{event: "session_shutdown", presence: "gone"},
	}

	if !slices.Equal(got, want) {
		t.Fatalf("captured reports =\n%#v\nwant\n%#v", got, want)
	}
}
