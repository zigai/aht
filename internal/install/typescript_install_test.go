package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func writeStalePiExtension(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "extensions", piExtensionName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `"aht managed integration";
"AHT_INTEGRATION_ID=pi";
"AHT_INTEGRATION_VERSION=5";
`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallPiWritesExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	path := writeStalePiExtension(t, dir)

	result, err := Run(Options{
		Harness:      registry.Harness("pi"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected pi install to report changed")
	}
	if result.Path != path {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		`on("agent_start"`,
		`on("before_agent_start"`,
		`on("ui_prompt_start"`,
		`report("waiting", ctx, event)`,
		`on("ui_prompt_end"`,
		`report(ctx.isIdle?.() ? "idle" : "running", ctx, event)`,
		"AHT_INTEGRATION_ID=pi",
		`"report", "pi"`,
		`"--observed-at", observedAt`,
		`addAttribute(args, "pi_prompt_kind", event?.kind)`,
		`args.push("--session-id", currentSessionId)`,
		`args.push("--session-path", currentSessionPath)`,
	}, "pi extension")
	if strings.Contains(result.Snippet, `on("tool_approval_`) {
		t.Fatalf("Pi extension must use documented UI prompt events: %q", result.Snippet)
	}
}

func TestInstallOmpWritesExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	extensions := filepath.Join(dir, "extensions")
	if err := os.MkdirAll(extensions, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := "\"aht managed integration\";\n\"AHT_INTEGRATION_ID=omp\";\n\"AHT_INTEGRATION_VERSION=15\";\n"
	if err := os.WriteFile(filepath.Join(extensions, ompExtensionName), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("omp"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected oh-my-pi install to report changed")
	}
	if result.Path != filepath.Join(dir, "extensions", ompExtensionName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=omp",
		`on("session_start"`,
		`on("agent_end"`,
		`on("tool_approval_requested"`,
		`on("tool_approval_resolved"`,
		`on("tool_execution_start"`,
		`on("tool_execution_end"`,
		`on("session_stop"`,
		`on("session_shutdown"`,
		`export default function`,
		"AHT_INTEGRATION_VERSION=17",
	}, "oh-my-pi extension")
	if strings.Contains(result.Snippet, `on("input"`) {
		t.Fatalf("OMP extension must not treat local interactive input as agent activity: %q", result.Snippet)
	}
	if strings.Contains(result.Snippet, `"--queue"`) {
		t.Fatalf("OMP extension must report through the broker hot path: %q", result.Snippet)
	}
	reinstalled, err := Run(Options{Harness: registry.Harness("omp"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	if reinstalled.Changed {
		t.Fatal("reinstalling the current OMP extension must be idempotent")
	}
}

func TestPiAndOmpRefuseToOverwriteSharedExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	piResult, err := Run(Options{Harness: registry.Harness("pi"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(piResult.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{Harness: registry.Harness("omp"), Binary: testInstallBinary}); !errors.Is(err, errForeignFile) {
		t.Fatalf("OMP overwrite error = %v, want errForeignFile", err)
	}
	current, err := os.ReadFile(piResult.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) || !strings.Contains(string(current), "AHT_INTEGRATION_ID=pi") {
		t.Fatalf("Pi extension changed after refused OMP install: %s", current)
	}
}

func TestInstallOmpUsesProfileAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", ".omp")
	t.Setenv("OMP_PROFILE", "work")
	t.Setenv("PI_PROFILE", "")

	result, err := Run(Options{
		Harness:      registry.Harness("omp"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantPath := filepath.Join(home, ".omp", "profiles", "work", "agent", "extensions", ompExtensionName)
	if result.Path != wantPath {
		t.Fatalf("unexpected profile path %q, want %q", result.Path, wantPath)
	}
}

func TestInstallOpenCodeWritesPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	result, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected opencode install to report changed")
	}
	if result.Path != filepath.Join(dir, "opencode", "plugins", opencodePluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=opencode",
		"AHT_INTEGRATION_VERSION=12",
		`export default { id: "aht-state", setup, server };`,
		`async function server(ctx: V1PluginContext)`,
		`async function setup(ctx: V2PluginContext)`,
		`event: async ({ event }`,
		`ctx?.event?.subscribe`,
		`ctx?.location?.directory`,
		`"permission.asked"`,
		`"session.deleted"`,
		`state === "gone" ? "--presence"`,
		`"--observed-at", observedAt`,
	}, "opencode plugin")
}

func TestInstallOpenCodeReplacesManagedPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "opencode", "plugins", opencodePluginName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating opencode plugin dir: %v", err)
	}
	oldPlugin := `"aht managed integration";
const old = "old-aht";
`
	if err := os.WriteFile(path, []byte(oldPlugin), 0o600); err != nil {
		t.Fatalf("writing old plugin: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected opencode install to replace old managed plugin")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed plugin: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed plugin to be removed: %s", text)
	}
	second, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second opencode install to be idempotent")
	}
}

func TestInstallKiloWritesPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	result, err := Run(Options{
		Harness:      registry.Harness("kilo"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kilo install to report changed")
	}
	if result.Path != filepath.Join(dir, "kilo", "plugin", kiloPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=kilo",
		`export default { id: "aht-state", server: AHTPlugin };`,
		`event: async ({ event }`,
		`"permission.asked"`,
		`"session.deleted"`,
		`state === "gone" ? "--presence"`,
		`"AHT_INTEGRATION_VERSION=9"`,
		`"--observed-at", observedAt`,
		`"kilo_status"`,
		`"--reporter", source`,
	}, "kilo snippet")
}

func TestInstallKiloReplacesManagedPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "kilo", "plugin", kiloPluginName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating kilo plugin dir: %v", err)
	}
	oldPlugin := `"aht managed integration";
const old = "old-aht";
`
	if err := os.WriteFile(path, []byte(oldPlugin), 0o600); err != nil {
		t.Fatalf("writing old plugin: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("kilo"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kilo install to replace old managed plugin")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed plugin: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed plugin to be removed: %s", text)
	}
	second, err := Run(Options{
		Harness:      registry.Harness("kilo"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second kilo install to be idempotent")
	}
}

func TestInstallAmpWritesPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	result, err := Run(Options{
		Harness:      registry.Harness("amp"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected amp install to report changed")
	}
	if result.Path != filepath.Join(dir, "amp", "plugins", ampPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{
		"AHT_INTEGRATION_ID=amp",
		`export default function (amp: PluginAPI)`,
		`amp.on("session.start"`,
		`amp.on("agent.start"`,
		`amp.on("tool.call"`,
		`amp.on("agent.end"`,
		`state === "gone" ? "--presence"`,
		`"AHT_INTEGRATION_VERSION=3"`,
		`"--observed-at", observedAt`,
		`"report", "amp"`,
		`"--reporter", source`,
	}, "amp snippet")
}

func TestInstallAmpReplacesManagedPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "amp", "plugins", ampPluginName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating amp plugin dir: %v", err)
	}
	oldPlugin := `"aht managed integration";
const old = "old-aht";
`
	if err := os.WriteFile(path, []byte(oldPlugin), 0o600); err != nil {
		t.Fatalf("writing old plugin: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("amp"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected amp install to replace old managed plugin")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed plugin: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed plugin to be removed: %s", text)
	}
	second, err := Run(Options{
		Harness:      registry.Harness("amp"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second amp install to be idempotent")
	}
}
