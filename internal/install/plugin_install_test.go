package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestInstallAgyWritesPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	result, err := Run(t.Context(), Options{
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
	if result.Path != filepath.Join(home, ".gemini", "config", "plugins", agyPluginName) {
		t.Fatalf("unexpected path %q", result.Path)
	}
	requireTextContainsAll(t, result.Snippet, []string{"hook agy"}, "agy snippet")
	requireAgyPluginManifest(t, result.Path)
	requireAgyPluginHooks(t, result.Path)
	requireAgyPluginMarker(t, result.Path)
	requireAgyImportManifest(t, filepath.Join(home, ".gemini", "config", agyImportManifestName))

	second, err := Run(t.Context(), Options{
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
	pluginDir := filepath.Join(home, ".gemini", "config", "plugins", agyPluginName)
	if err := os.MkdirAll(pluginDir, 0o700); err != nil {
		t.Fatalf("creating agy plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"foreign"}`), 0o600); err != nil {
		t.Fatalf("writing foreign plugin manifest: %v", err)
	}

	_, err := Run(t.Context(), Options{
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

	result, err := Run(t.Context(), Options{
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

const (
	legacyAgyMarker   = "aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=9"
	legacyAgyManifest = `{"imports":[` +
		`{"name":"aht-state","source":"antigravity","imported_at":"2026-01-01T00:00:00Z","components":["hooks"]},` +
		`{"name":"other-plugin","source":"antigravity","imported_at":"2026-01-02T00:00:00Z","components":["skills"]}]}`
)

func writeLegacyAgyInstall(t *testing.T, home string, marker string) (string, string) {
	t.Helper()

	legacyRoot := filepath.Join(home, ".gemini", "antigravity-cli")
	legacyPlugin := filepath.Join(legacyRoot, "plugins", agyPluginName)
	files := map[string]string{
		filepath.Join(legacyPlugin, "plugin.json"):                          `{"name":"aht-state"}`,
		filepath.Join(legacyPlugin, "hooks.json"):                           `{}`,
		filepath.Join(legacyRoot, "plugins", "other-plugin", "plugin.json"): `{"name":"other-plugin"}`,
		filepath.Join(legacyRoot, agyImportManifestName):                    legacyAgyManifest,
	}
	if marker != "" {
		files[filepath.Join(legacyPlugin, agyMarkerFileName)] = marker
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	return legacyPlugin, filepath.Join(legacyRoot, agyImportManifestName)
}

func requireAgyImportNames(t *testing.T, path string, want []string) {
	t.Helper()

	manifest := decodeTestJSONObject(t, readTestFile(t, path, "reading agy import manifest"), "agy import manifest")
	imports, _ := manifest["imports"].([]any)
	got := make([]string, 0, len(imports))
	for _, value := range imports {
		item, _ := value.(map[string]any)
		name, _ := item["name"].(string)
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("agy imports in %s = %v, want %v", path, got, want)
	}
}

func TestUpgradeMovesAgyPluginFromLegacyDirectory(t *testing.T) {
	home := isolateUpgradeHome(t)
	legacyPlugin, legacyManifest := writeLegacyAgyInstall(t, home, legacyAgyMarker)
	nativePlugin := filepath.Join(home, ".gemini", "config", "plugins", agyPluginName)

	status, err := Inspect(t.Context(), registry.Harness("agy"), testInstallBinary)
	if err != nil || status.Status != ArtifactStale {
		t.Fatalf("status with only the legacy install = %+v, %v; want stale", status, err)
	}

	requireAgyUpgrade(t, true, true)
	requireAgyUpgrade(t, false, true)

	requireAgyPluginHooks(t, nativePlugin)
	requireAgyPluginMarker(t, nativePlugin)
	requireAgyImportManifest(t, filepath.Join(home, ".gemini", "config", agyImportManifestName))
	requireLegacyAgyRetired(t, legacyPlugin, legacyManifest)

	requireAgyUpgrade(t, false, false)
	status, err = Inspect(t.Context(), registry.Harness("agy"), testInstallBinary)
	if err != nil || status.Status != ArtifactCurrent {
		t.Fatalf("status after upgrade = %+v, %v; want current", status, err)
	}
}

func requireAgyUpgrade(t *testing.T, dryRun bool, wantChanged bool) {
	t.Helper()

	results, err := Upgrade(t.Context(), testInstallBinary, dryRun)
	if err != nil || len(results) != 1 || results[0].Harness != "agy" || results[0].Changed != wantChanged {
		t.Fatalf("upgrade(dryRun=%v) = %+v, %v; want changed=%v", dryRun, results, err, wantChanged)
	}
}

func requireLegacyAgyRetired(t *testing.T, legacyPlugin string, legacyManifest string) {
	t.Helper()

	if _, err := os.Stat(legacyPlugin); !os.IsNotExist(err) {
		t.Fatalf("legacy managed plugin remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(legacyPlugin), "other-plugin", "plugin.json")); err != nil {
		t.Fatalf("a foreign plugin next to the legacy one was removed: %v", err)
	}
	requireAgyImportNames(t, legacyManifest, []string{"other-plugin"})
}

func TestInstallAgyLeavesForeignLegacyPluginAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyPlugin, legacyManifest := writeLegacyAgyInstall(t, home, "")

	if _, err := Run(t.Context(), Options{Harness: registry.Harness("agy"), Binary: testInstallBinary}); err != nil {
		t.Fatalf("install next to a foreign legacy plugin: %v", err)
	}

	if got := readTestFile(t, filepath.Join(legacyPlugin, "plugin.json"), "reading foreign legacy plugin"); string(got) != `{"name":"aht-state"}` {
		t.Fatalf("foreign legacy plugin changed: %s", got)
	}
	if got := readTestFile(t, legacyManifest, "reading legacy manifest"); string(got) != legacyAgyManifest {
		t.Fatalf("legacy manifest changed: %s", got)
	}
}

func TestRemoveAgyAlsoRemovesLegacyPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacyPlugin, legacyManifest := writeLegacyAgyInstall(t, home, legacyAgyMarker)
	if _, err := Run(t.Context(), Options{Harness: registry.Harness("agy"), Binary: testInstallBinary}); err != nil {
		t.Fatal(err)
	}
	writeLegacyAgyInstall(t, home, legacyAgyMarker)

	removed, err := Remove(t.Context(), Options{Harness: registry.Harness("agy"), Binary: testInstallBinary})
	if err != nil || !removed.Changed {
		t.Fatalf("remove = %+v, %v", removed, err)
	}

	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "plugins", agyPluginName)); !os.IsNotExist(err) {
		t.Fatalf("native plugin remains after removal: %v", err)
	}
	if _, err := os.Stat(legacyPlugin); !os.IsNotExist(err) {
		t.Fatalf("legacy plugin remains after removal: %v", err)
	}
	requireAgyImportNames(t, legacyManifest, []string{"other-plugin"})
	requireAgyImportNames(t, filepath.Join(home, ".gemini", "config", agyImportManifestName), nil)
}

func TestUpgradeKeepsLegacyManifestDataAgyOwns(t *testing.T) {
	home := isolateUpgradeHome(t)
	_, legacyManifest := writeLegacyAgyInstall(t, home, legacyAgyMarker)
	writeAgyManifest(t, legacyManifest, agyManifestWithExtras)

	requireAgyUpgrade(t, false, true)

	manifest, entries := decodeAgyManifestEntries(t, legacyManifest)
	requireAgyManifestExtras(t, manifest)
	requireJSONEqual(t, entries["other-plugin"], foreignAgyEntry, "foreign legacy entry")
	if _, exists := entries[agyPluginName]; exists || len(entries) != 1 {
		t.Fatalf("legacy manifest entries = %#v, want only the foreign entry", entries)
	}
}

func TestInstallAgyPreservesNativeImportEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifestPath := filepath.Join(home, ".gemini", "config", agyImportManifestName)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	native := `{"imports":[{"name":"other-plugin","source":"antigravity","importedAt":"2026-01-02T03:04:05Z","components":["skills"]}]}`
	if err := os.WriteFile(manifestPath, []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(t.Context(), Options{Harness: registry.Harness("agy"), Binary: testInstallBinary}); err != nil {
		t.Fatal(err)
	}

	data := readTestFile(t, manifestPath, "reading agy import manifest")
	requireTextContainsAll(t, string(data), []string{`"importedAt": "2026-01-02T03:04:05Z"`}, "agy import manifest")
	requireAgyImportNames(t, manifestPath, []string{"other-plugin", agyPluginName})
}

const (
	foreignAgyEntry = `{"name":"other-plugin","source":"marketplace","importedAt":"2026-01-02T03:04:05Z","components":["skills"],` +
		`"marketplace":{"url":"https://example.invalid/m","tags":["a","b"],"stars":3},"revision":1.5,"note":null}`
	ownAgyEntry = `{"name":"aht-state","source":"antigravity","importedAt":"2026-01-01T00:00:00Z","components":["hooks"],` +
		`"pinned":true,"metadata":{"channel":"stable","build":7}}`
	agyManifestWithExtras = `{"version":2,"settings":{"mirror":["x","y"],"limit":10},"imports":[` + foreignAgyEntry + `,` + ownAgyEntry + `]}`
)

func writeAgyManifest(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeAgyManifestEntries(t *testing.T, path string) (map[string]any, map[string]map[string]any) {
	t.Helper()

	manifest := decodeTestJSONObject(t, readTestFile(t, path, "reading agy import manifest"), "agy import manifest")
	imports, _ := manifest["imports"].([]any)
	entries := make(map[string]map[string]any, len(imports))
	for _, value := range imports {
		entry, _ := value.(map[string]any)
		name, _ := entry["name"].(string)
		entries[name] = entry
	}

	return manifest, entries
}

func requireJSONEqual(t *testing.T, got any, wantJSON string, description string) {
	t.Helper()

	want := decodeTestJSONObject(t, []byte(wantJSON), description)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed:\n got  %#v\n want %#v", description, got, want)
	}
}

func requireAgyManifestExtras(t *testing.T, manifest map[string]any) {
	t.Helper()

	if manifest["version"] != float64(2) {
		t.Fatalf("top-level version lost: %#v", manifest)
	}
	requireJSONEqual(t, manifest["settings"], `{"mirror":["x","y"],"limit":10}`, "top-level settings")
}

func TestAgyImportManifestKeepsDataAgyOwns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifestPath := filepath.Join(home, ".gemini", "config", agyImportManifestName)
	writeAgyManifest(t, manifestPath, agyManifestWithExtras)
	options := Options{Harness: registry.Harness("agy"), Binary: testInstallBinary}

	if _, err := Run(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	second, err := Run(t.Context(), options)
	if err != nil || second.Changed {
		t.Fatalf("reinstall = %+v, %v; want unchanged", second, err)
	}
	manifest, entries := decodeAgyManifestEntries(t, manifestPath)
	requireAgyManifestExtras(t, manifest)
	requireJSONEqual(t, entries["other-plugin"], foreignAgyEntry, "foreign entry after install")
	requireJSONEqual(t, entries[agyPluginName], ownAgyEntry, "aht entry after install")

	if _, err := Remove(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	manifest, entries = decodeAgyManifestEntries(t, manifestPath)
	requireAgyManifestExtras(t, manifest)
	requireJSONEqual(t, entries["other-plugin"], foreignAgyEntry, "foreign entry after remove")
	if _, exists := entries[agyPluginName]; exists || len(entries) != 1 {
		t.Fatalf("remove left aht entry or dropped others: %#v", entries)
	}
}

func TestAgyImportManifestFillsOnlyAhtFieldsOfItsEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifestPath := filepath.Join(home, ".gemini", "config", agyImportManifestName)
	writeAgyManifest(t, manifestPath, `{"imports":[{"name":"aht-state","source":"elsewhere","components":["skills"],"pinned":true,"metadata":{"build":7}}]}`)

	if _, err := Run(t.Context(), Options{Harness: registry.Harness("agy"), Binary: testInstallBinary}); err != nil {
		t.Fatal(err)
	}

	_, entries := decodeAgyManifestEntries(t, manifestPath)
	entry := entries[agyPluginName]
	if entry["source"] != agyImportSource || entry["pinned"] != true {
		t.Fatalf("aht entry = %#v", entry)
	}
	if importedAt, _ := entry["importedAt"].(string); importedAt == "" {
		t.Fatalf("importedAt was not filled: %#v", entry)
	}
	if !reflect.DeepEqual(entry["components"], []any{"skills", "hooks"}) {
		t.Fatalf("components = %#v, want existing component kept and hooks added", entry["components"])
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

	result, err := Run(t.Context(), Options{
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

	second, err := Run(t.Context(), Options{
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
		"beforeRun(context)",
		"beforeTool(context)",
		"afterTool(context)",
		"afterRun({ snapshot, result })",
		"export default plugin",
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

	_, err := Run(t.Context(), Options{
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

	result, err := Run(t.Context(), Options{
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

	second, err := Run(t.Context(), Options{
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
