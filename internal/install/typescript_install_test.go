package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
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

	result, err := Run(t.Context(), Options{
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
		`on("ui_prompt_end"`,
		"AHT_INTEGRATION_ID=pi",
	}, "pi extension")
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

	result, err := Run(t.Context(), Options{
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
	}, "oh-my-pi extension")
	reinstalled, err := Run(t.Context(), Options{Harness: registry.Harness("omp"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	if reinstalled.Changed {
		t.Fatal("reinstalling the current OMP extension must be idempotent")
	}
	if reinstalled.Message != "already installed" {
		t.Fatalf("reinstalled message = %q, want 'already installed'", reinstalled.Message)
	}
}

func TestPiAndOmpRefuseToOverwriteSharedExtension(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)

	piResult, err := Run(t.Context(), Options{Harness: registry.Harness("pi"), Binary: testInstallBinary})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(piResult.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), Options{Harness: registry.Harness("omp"), Binary: testInstallBinary}); !errors.Is(err, errForeignFile) {
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

	result, err := Run(t.Context(), Options{
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

func TestInstallOmpUsesConfigDirName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CONFIG_DIR", ".custom-omp")
	t.Setenv("OMP_PROFILE", "")

	result, err := Run(t.Context(), Options{Harness: registry.Harness("omp"), Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	wantPath := filepath.Join(home, ".custom-omp", "agent", "extensions", ompExtensionName)
	if result.Path != wantPath {
		t.Fatalf("unexpected path %q, want %q", result.Path, wantPath)
	}
}

func TestInstallWritesPlugin(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		path    []string
		want    []string
	}{
		{
			harness: "opencode",
			path:    []string{"opencode", "plugins", opencodePluginName},
			want: []string{
				"AHT_INTEGRATION_ID=opencode",
				`export default { id: "aht-state", setup, server };`,
				`event: async ({ event }`,
				`"permission.asked"`,
				`"session.deleted"`,
			},
		},
		{
			harness: "kilo",
			path:    []string{"kilo", "plugin", kiloPluginName},
			want: []string{
				"AHT_INTEGRATION_ID=kilo",
				`export default { id: "aht-state", server: AHTPlugin };`,
				`event: async ({ event }`,
				`"permission.asked"`,
				`"session.deleted"`,
			},
		},
		{
			harness: "amp",
			path:    []string{"amp", "plugins", ampPluginName},
			want: []string{
				"AHT_INTEGRATION_ID=amp",
				`export default function (amp: PluginAPI)`,
				`amp.on("session.start"`,
				`amp.on("agent.start"`,
				`amp.on("tool.call"`,
				`amp.on("agent.end"`,
			},
		},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)

			result, err := Run(t.Context(), Options{
				Harness:      test.harness,
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
				t.Fatalf("expected %s install to report changed", test.harness)
			}
			if result.Path != filepath.Join(append([]string{dir}, test.path...)...) {
				t.Fatalf("unexpected path %q", result.Path)
			}
			requireTextContainsAll(t, result.Snippet, test.want, string(test.harness)+" plugin")
		})
	}
}

func TestInstallReplacesManagedPlugin(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		path    []string
	}{
		{harness: "opencode", path: []string{"opencode", "plugins", opencodePluginName}},
		{harness: "kilo", path: []string{"kilo", "plugin", kiloPluginName}},
		{harness: "amp", path: []string{"amp", "plugins", ampPluginName}},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			requirePluginReplacement(t, test.harness, test.path)
		})
	}
}

func requirePluginReplacement(t *testing.T, harness registry.Harness, pathParts []string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(append([]string{dir}, pathParts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating %s plugin dir: %v", harness, err)
	}
	oldPlugin := `"aht managed integration";
const old = "old-aht";
`
	if err := os.WriteFile(path, []byte(oldPlugin), 0o600); err != nil {
		t.Fatalf("writing old plugin: %v", err)
	}

	result, err := Run(t.Context(), Options{
		Harness:      harness,
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
		t.Fatalf("expected %s install to replace old managed plugin", harness)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed plugin: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed plugin to be removed: %s", text)
	}
	second, err := Run(t.Context(), Options{
		Harness:      harness,
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
		t.Fatalf("expected second %s install to be idempotent", harness)
	}
}
