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
