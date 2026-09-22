package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zigai/aht/internal/harness"
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
		t.Fatal("rendered opencode template is empty")
	}
	placeholderPattern := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)
	if match := placeholderPattern.FindString(rendered); match != "" {
		t.Fatalf("rendered opencode template contains unresolved placeholder %q:\n%s", match, rendered)
	}
}

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_DIR", "/tmp/opencode-config")
	t.Setenv("OPENCODE_CONFIG", "/tmp/ignored/config.json")
	if got := opencodeConfigDir(); got != "/tmp/opencode-config" {
		t.Fatalf("expected OPENCODE_CONFIG_DIR to win, got %q", got)
	}
}

func runOpencodeDriver(t *testing.T, driverSource string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated plugin")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	// Each invocation is a real subprocess. The driver deliberately ignores
	// event promises, matching OpenCode's native event dispatch.
	//nolint:gosec // test helper creates an executable reporter script
	if err := os.WriteFile(reporter, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$AHT_CAPTURE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	module := harness.RenderScriptTemplate(opencodePluginTemplate, opencodeIntegrationID, reporter, opencodeIntegrationSource, integrationVersion)
	if err := os.WriteFile(filepath.Join(dir, "plugin.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driverSource), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)
	command.Env = append(os.Environ(), "AHT_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("driver execution failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var observations []string
	var activity string
	fields := strings.Split(strings.TrimSpace(string(data)), "\n")
	for index := 0; index+1 < len(fields); index++ {
		switch fields[index] {
		case "--activity":
			activity = fields[index+1]
		case "--session-id":
			observations = append(observations, fields[index+1]+":"+activity)
		}
	}
	return observations
}

func TestPluginDisposalDrainsNativeEvents(t *testing.T) {
	driver := `import plugin from "./plugin.ts";
const hooks = await plugin.server({ directory: "/work" });
for (const [sessionID, type] of [["first", "busy"], ["first", "idle"], ["resumed", "busy"], ["resumed", "idle"]]) {
  void hooks.event({ event: { type: "session.status", properties: { sessionID, status: { type } } } });
}
await hooks.dispose();
process.exit(0);
`
	observations := runOpencodeDriver(t, driver)
	if got := strings.Join(observations, ","); got != "first:running,first:idle,resumed:running,resumed:idle" {
		t.Fatalf("native observations must finish in order before disposal returns: %s", got)
	}
}

func TestSetupDisposalDrainsNativeEvents(t *testing.T) {
	driver := `import plugin from "./plugin.ts";
let finish;
const finished = new Promise((resolve) => { finish = resolve; });
async function* generateEvents() {
  for (const [sessionID, type] of [["first", "busy"], ["first", "idle"], ["resumed", "busy"], ["resumed", "idle"]]) {
    yield { type: "session.status", data: { sessionID, status: { type } } };
  }
  finish();
}
const cleanup = await plugin.setup({
  location: { directory: "/work", project: { canonical: "/work" } },
  event: {
    subscribe: () => generateEvents(),
  },
});
await finished;
await cleanup();
process.exit(0);
`
	observations := runOpencodeDriver(t, driver)
	if got := strings.Join(observations, ","); got != "first:running,first:idle,resumed:running,resumed:idle" {
		t.Fatalf("native observations must finish in order before cleanup returns: %s", got)
	}
}

func TestPluginDefaultExportShape(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated plugin")
	}
	dir := t.TempDir()
	reporter := filepath.Join(dir, "reporter")
	//nolint:gosec // test helper creates an executable reporter script
	if err := os.WriteFile(reporter, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	module := harness.RenderScriptTemplate(opencodePluginTemplate, opencodeIntegrationID, reporter, opencodeIntegrationSource, integrationVersion)
	if err := os.WriteFile(filepath.Join(dir, "plugin.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := `import plugin from "./plugin.ts";
if (!plugin || typeof plugin !== "object") throw new Error("expected object export");
if (plugin.id !== "aht-state") throw new Error("expected id 'aht-state', got " + plugin.id);
if (typeof plugin.setup !== "function") throw new Error("expected setup function, got " + typeof plugin.setup);
if (typeof plugin.server !== "function") throw new Error("expected server function, got " + typeof plugin.server);
process.exit(0);
`
	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("shape validation failed: %v\n%s", err, output)
	}
}

func TestOpencodeV2EventStateTransitions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated plugin")
	}
	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	//nolint:gosec // test helper creates an executable reporter script
	if err := os.WriteFile(reporter, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$AHT_CAPTURE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	module := harness.RenderScriptTemplate(opencodePluginTemplate, opencodeIntegrationID, reporter, opencodeIntegrationSource, integrationVersion)
	if err := os.WriteFile(filepath.Join(dir, "plugin.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := `import plugin from "./plugin.ts";
let finish;
const finished = new Promise((resolve) => { finish = resolve; });
async function* generateEvents() {
  const events = [
    { type: "session.created", data: { sessionID: "s1" } },
    { type: "session.execution.started", data: { sessionID: "s1" } },
    { type: "permission.asked", data: { sessionID: "s1" } },
    { type: "permission.replied", data: { sessionID: "s1" } },
    { type: "session.status", data: { sessionID: "s1", status: { type: "busy" } } },
    { type: "session.status", data: { sessionID: "s1", status: { type: "retry" } } },
    { type: "session.status", data: { sessionID: "s1", status: { type: "idle" } } },
    { type: "session.execution.interrupted", data: { sessionID: "s1" } },
    { type: "session.execution.failed", data: { sessionID: "s1" } },
    { type: "session.execution.succeeded", data: { sessionID: "s1" } },
    { type: "unmapped.noise.event", data: { sessionID: "s1" } },
    { type: "session.deleted", data: { sessionID: "s1" } },
  ];
  for (const event of events) {
    yield event;
  }
  finish();
}
const cleanup = await plugin.setup({
  location: { directory: "/work", project: { canonical: "/work" } },
  event: {
    subscribe: () => generateEvents(),
  },
});
await finished;
await cleanup();
process.exit(0);
`
	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)
	command.Env = append(os.Environ(), "AHT_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("setup event state transitions: %v\n%s", err, output)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	var currentKind, currentVal string
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] == "--activity" || lines[i] == "--presence" {
			if i+1 < len(lines) {
				currentKind = strings.TrimPrefix(lines[i], "--")
				currentVal = lines[i+1]
				states = append(states, currentKind+":"+currentVal)
				i++
			}
		}
	}
	expected := []string{
		"activity:idle",
		"activity:running",
		"activity:waiting",
		"activity:running",
		"activity:running",
		"activity:running",
		"activity:idle",
		"activity:interrupted",
		"activity:failed",
		"activity:idle",
		"presence:gone",
	}
	if got := strings.Join(states, ","); got != strings.Join(expected, ",") {
		t.Fatalf("unexpected state sequence:\ngot:  %s\nwant: %s", got, strings.Join(expected, ","))
	}
}
