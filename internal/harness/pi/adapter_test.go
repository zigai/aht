package pi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

func TestPluginTemplateRendersCleanly(t *testing.T) {
	t.Parallel()

	h := New()
	plan := h.InstallPlan("/usr/local/bin/aht")
	if len(plan.Actions) == 0 {
		t.Fatal("expected at least one install action")
	}
	action, ok := plan.Actions[0].(harness.RenderedFileAction)
	if !ok {
		t.Fatalf("expected harness.RenderedFileAction, got %T", plan.Actions[0])
	}
	rendered := action.Plan.Content
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("rendered pi template is empty")
	}
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		t.Fatalf("rendered pi template contains unresolved placeholders:\n%s", rendered)
	}
}

//nolint:gocognit,cyclop // test drives multi-event sequence ordering and timeline reconciliation
func TestShutdownSurvivesEqualObservationTimestamps(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated extension")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	script := "#!" + node + "\nrequire('node:fs').appendFileSync(process.env.AHT_CAPTURE, JSON.stringify(process.argv.slice(2))+'\\n');\n"
	//nolint:gosec // test helper creates an executable reporter script
	if err := os.WriteFile(reporter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	module := harness.RenderScriptTemplate(piExtensionTemplate, piIntegrationID, reporter, piIntegrationSourceID, integrationVersion)
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
	store := registry.NewFileStore(filepath.Join(dir, "state.json"))
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
			Source:     registry.ObservationSourceNative,
			Evidence:   registry.ObservationEvidenceNativeEvent,
			Harness:    registry.HarnessPi,
			Identity:   registry.ObservationIdentity{SessionID: "native-session"},
			Attributes: map[string]string{"aht_integration": piIntegrationSourceID},
			ObservedAt: at,
		}
		for index := 0; index+1 < len(args); index++ {
			switch args[index] {
			case "--event":
				observation.NativeEvent = args[index+1]
			case "--activity":
				activity := registry.Activity(args[index+1])
				observation.Activity = &activity
			case "--presence":
				presence := registry.Presence(args[index+1])
				observation.Presence = &presence
			case "--sequence":
				sequence, err := strconv.ParseUint(args[index+1], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				observation.Sequence = &sequence
			}
		}
		session, err = store.Observe(t.Context(), observation)
		if err != nil {
			t.Fatalf("native %s was lost: %v", observation.NativeEvent, err)
		}
	}
	if session.Presence != registry.PresenceGone || session.Observations.Native == nil || session.Observations.Native.Event != "session_shutdown" {
		t.Fatalf("shutdown was not retained as native terminal evidence: %#v", session)
	}
}
