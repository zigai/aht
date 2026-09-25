package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestInstallAgyWritesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(Options{
		Harness:      registry.Harness("agy"),
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
		t.Fatal("expected agy install to report changed")
	}
	if result.Path != filepath.Join(home, ".gemini", "antigravity-cli", "plugins", agyPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{"hook agy"}, "agy snippet")
	requireAgyPluginManifest(t, result.Path)
	requireAgyPluginHooks(t, result.Path)
	requireAgyPluginMarker(t, result.Path)
	requireAgyImportManifest(t, filepath.Join(home, ".gemini", "antigravity-cli", agyImportManifestName))

	second, err := Run(Options{
		Harness:      registry.Harness("agy"),
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
		t.Fatal("expected second agy install to be idempotent")
	}
}

func TestInstallAgyRequiresForceForForeignPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginDir := filepath.Join(home, ".gemini", "antigravity-cli", "plugins", agyPluginName)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("creating agy plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"foreign"}`), 0o600); err != nil {
		t.Fatalf("writing foreign plugin manifest: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.Harness("agy"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged agy plugin")
	}

	result, err := Run(Options{
		Harness:      registry.Harness("agy"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        true,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("forced Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected forced agy install to report changed")
	}
}

func requireAgyPluginManifest(t *testing.T, dir string) {
	t.Helper()

	manifestData := readTestFile(t, filepath.Join(dir, "plugin.json"), "reading plugin manifest")
	manifest := decodeTestJSONObject(t, manifestData, "plugin manifest")
	if manifest["name"] != agyPluginName {
		t.Fatalf("expected plugin name %q, got %#v", agyPluginName, manifest["name"])
	}
}

func requireAgyPluginHooks(t *testing.T, dir string) {
	t.Helper()

	hooksData := readTestFile(t, filepath.Join(dir, "hooks.json"), "reading agy hooks")
	if !strings.Contains(string(hooksData), testInstallBinary+" --json hook agy") {
		t.Fatalf("expected agy hooks to request protocol JSON explicitly: %s", hooksData)
	}
	hooks := decodeTestJSONObject(t, hooksData, "agy hooks")
	pluginHooks, hooksOK := hooks[agyPluginName].(map[string]any)
	if !hooksOK {
		t.Fatalf("expected %s hook namespace, got %#v", agyPluginName, hooks)
	}
	requireTestHookEvents(t, pluginHooks, []string{"PreInvocation", "PostInvocation", "PreToolUse", "PostToolUse", hookEventStop})
}

func requireAgyPluginMarker(t *testing.T, dir string) {
	t.Helper()

	marker := readTestFile(t, filepath.Join(dir, agyMarkerFileName), "reading agy marker")
	if !strings.Contains(string(marker), managedMarker) {
		t.Fatalf("expected managed marker, got %q", marker)
	}
	if !strings.Contains(string(marker), "AHT_INTEGRATION_VERSION=9") {
		t.Fatalf("expected agy integration version 9 marker, got %q", marker)
	}
}

func requireAgyImportManifest(t *testing.T, path string) {
	t.Helper()

	data := readTestFile(t, path, "reading agy import manifest")
	manifest := decodeTestJSONObject(t, data, "agy import manifest")
	imports, importsOK := manifest["imports"].([]any)
	if !importsOK {
		t.Fatalf("expected agy imports list, got %#v", manifest)
	}

	for _, importValue := range imports {
		importItem, importOK := importValue.(map[string]any)
		if !importOK || importItem["name"] != agyPluginName {
			continue
		}
		if importItem["source"] != agyImportSource {
			t.Fatalf("expected agy import source %q, got %#v", agyImportSource, importItem["source"])
		}
		components, componentsOK := importItem["components"].([]any)
		if !componentsOK {
			t.Fatalf("expected agy import components, got %#v", importItem["components"])
		}
		for _, component := range components {
			if component == agyImportComponent {
				return
			}
		}
		t.Fatalf("expected agy import component %q, got %#v", agyImportComponent, components)
	}

	t.Fatalf("expected agy import for %q, got %#v", agyPluginName, imports)
}

func TestInstallClineWritesNativePlugin(t *testing.T) {
	clineDir := filepath.Join(t.TempDir(), ".cline")
	t.Setenv("CLINE_DIR", clineDir)
	pluginDir := filepath.Join(clineDir, "plugins", "aht-state")

	result, err := Run(Options{
		Harness:      registry.Harness("cline"),
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
		t.Fatal("expected cline install to report changed")
	}
	if result.Path != pluginDir {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireClinePackageManifest(t, pluginDir)
	requireClineAgentPlugin(t, pluginDir)
	requireClinePluginMarker(t, pluginDir)

	second, err := Run(Options{
		Harness:      registry.Harness("cline"),
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
		t.Fatal("expected second cline install to be idempotent")
	}
}

func requireClinePackageManifest(t *testing.T, pluginDir string) {
	t.Helper()
	packageData := readTestFile(t, filepath.Join(pluginDir, "package.json"), "reading Cline plugin package")
	var packageManifest map[string]any
	if err := json.Unmarshal(packageData, &packageManifest); err != nil {
		t.Fatalf("parsing Cline plugin package: %v", err)
	}
	clineManifest, ok := packageManifest["cline"].(map[string]any)
	if !ok {
		t.Fatalf("Cline package manifest missing cline entry: %#v", packageManifest)
	}
	plugins, ok := clineManifest["plugins"].([]any)
	if !ok || len(plugins) != 1 {
		t.Fatalf("Cline package plugin entries = %#v", clineManifest["plugins"])
	}
	entry, ok := plugins[0].(map[string]any)
	if !ok {
		t.Fatalf("Cline package plugin entry = %#v", plugins[0])
	}
	if paths, ok := entry["paths"].([]any); !ok || len(paths) != 1 || paths[0] != "./index.js" {
		t.Fatalf("Cline plugin paths = %#v", entry["paths"])
	}
	if capabilities, ok := entry["capabilities"].([]any); !ok || len(capabilities) != 1 || capabilities[0] != "hooks" {
		t.Fatalf("Cline plugin capabilities = %#v", entry["capabilities"])
	}
}

func requireClineAgentPlugin(t *testing.T, pluginDir string) {
	t.Helper()
	text := string(readTestFile(t, filepath.Join(pluginDir, "index.js"), "reading Cline AgentPlugin"))
	requireTextContainsAll(t, text, []string{
		"manifest: { capabilities: [\"hooks\"] }",
		"setup(_api, ctx)",
		"beforeRun(context)",
		"beforeTool(context)",
		"afterTool(context)",
		"afterRun({ snapshot, result })",
		"export default plugin",
		"ctx?.session?.sessionId",
		"ctx?.workspaceInfo?.rootPath",
		"snapshot.runId",
		"--pid",
		`"--reporter", "cline-plugin"`,
	}, "Cline AgentPlugin")
	if strings.Contains(text, "context.input") || strings.Contains(text, "context.result") || strings.Contains(text, "outputText") {
		t.Fatalf("Cline plugin reads content-bearing fields: %q", text)
	}
}

func requireClinePluginMarker(t *testing.T, pluginDir string) {
	t.Helper()
	marker := string(readTestFile(t, filepath.Join(pluginDir, ".aht-managed"), "reading Cline plugin marker"))
	requireTextContainsAll(t, marker, []string{
		managedMarker,
		"AHT_INTEGRATION_ID=cline",
		"AHT_SOURCE=cline-plugin",
	}, "Cline plugin marker")
}

func TestInstallClineRequiresForceForForeignPlugin(t *testing.T) {
	clineDir := filepath.Join(t.TempDir(), ".cline")
	t.Setenv("CLINE_DIR", clineDir)
	pluginDir := filepath.Join(clineDir, "plugins", "aht-state")
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("creating Cline plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("writing foreign Cline plugin: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.Harness("cline"),
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged Cline plugin")
	}
}

func TestInstallGooseWritesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pluginPath := filepath.Join(home, ".agents", "plugins", goosePluginName)
	if err := os.MkdirAll(pluginPath, 0o700); err != nil {
		t.Fatal(err)
	}
	staleMarker := "aht managed integration\nAHT_INTEGRATION_ID=goose\nAHT_INTEGRATION_VERSION=5\n"
	if err := os.WriteFile(filepath.Join(pluginPath, gooseMarkerFileName), []byte(staleMarker), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("goose"),
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
		t.Fatal("expected goose install to report changed")
	}
	if result.Path != pluginPath {
		t.Fatalf("unexpected path %q", result.Path)
	}

	requireGoosePluginManifest(t, result.Path)
	requireGoosePluginHooks(t, result.Path)
	requireGoosePluginScript(t, result.Path)
	requireGoosePluginMarker(t, result.Path)

	second, err := Run(Options{
		Harness:      registry.Harness("goose"),
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
		t.Fatal("expected second goose install to be idempotent")
	}
}

func requireGoosePluginManifest(t *testing.T, dir string) {
	t.Helper()

	manifestData := readTestFile(t, filepath.Join(dir, "plugin.json"), "reading goose plugin manifest")
	manifest := decodeTestJSONObject(t, manifestData, "goose plugin manifest")
	if manifest["name"] != goosePluginName {
		t.Fatalf("expected plugin name %q, got %#v", goosePluginName, manifest["name"])
	}
	if manifest["description"] != managedMarker {
		t.Fatalf("expected managed marker description, got %#v", manifest["description"])
	}
}

func requireGoosePluginHooks(t *testing.T, dir string) {
	t.Helper()

	hooksData := readTestFile(t, filepath.Join(dir, "hooks", "hooks.json"), "reading goose hooks")
	hooksConfig := decodeTestJSONObject(t, hooksData, "goose hooks")
	hooks := requireTestHooks(t, hooksConfig)
	requireTestHookEvents(t, hooks, []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		"BeforeReadFile",
		"AfterFileEdit",
		"BeforeShellExecution",
		"AfterShellExecution",
		hookEventStop,
		"SessionEnd",
	})

	text := string(hooksData)
	requireTextContainsAll(t, text, []string{
		"${PLUGIN_ROOT}/scripts/report.sh",
	}, "goose hooks")
}

func requireGoosePluginScript(t *testing.T, dir string) {
	t.Helper()

	text := string(readTestFile(t, filepath.Join(dir, "scripts", "report.sh"), "reading goose report script"))
	requireTextContainsAll(t, text, []string{
		managedMarker,
		"--raw-stdin-defaults-only",
		"--reporter goose-hook",
		`--presence "$transition"`,
		`--activity "$transition"`,
		`--event "$event"`,
	}, "goose report script")
}

func requireGoosePluginMarker(t *testing.T, dir string) {
	t.Helper()

	marker := readTestFile(t, filepath.Join(dir, gooseMarkerFileName), "reading goose marker")
	if !strings.Contains(string(marker), managedMarker) {
		t.Fatalf("expected managed marker, got %q", marker)
	}
}
