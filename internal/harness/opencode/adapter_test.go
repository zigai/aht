package opencode

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		t.Fatalf("rendered opencode template contains unresolved placeholders:\n%s", rendered)
	}
}

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_DIR", "/tmp/opencode-config")
	t.Setenv("OPENCODE_CONFIG", "/tmp/ignored/config.json")
	if got := openCodeConfigDir(); got != "/tmp/opencode-config" {
		t.Fatalf("expected OPENCODE_CONFIG_DIR to win, got %q", got)
	}
}

func TestPluginDisposalDrainsNativeEvents(t *testing.T) {
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
	module := harness.RenderScriptTemplate(openCodePluginTemplate, openCodeIntegrationID, reporter, openCodeIntegrationSource, integrationVersion)
	if err := os.WriteFile(filepath.Join(dir, "plugin.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := `import plugin from "./plugin.ts";
const hooks = await plugin.server({ directory: "/work" });
for (const [sessionID, type] of [["first", "busy"], ["first", "idle"], ["resumed", "busy"], ["resumed", "idle"]]) {
  void hooks.event({ event: { type: "session.status", properties: { sessionID, status: { type } } } });
}
await hooks.dispose();
process.exit(0);
`
	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)
	command.Env = append(os.Environ(), "AHT_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("plugin disposal: %v\n%s", err, output)
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
	if got := strings.Join(observations, ","); got != "first:running,first:idle,resumed:running,resumed:idle" {
		t.Fatalf("native observations must finish in order before disposal returns: %s", got)
	}
}
