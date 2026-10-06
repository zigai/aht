package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestLocationsForReportsExistingPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	locations, ok := harness.LocationsFor(registry.Harness("codex"), projectDir)
	if !ok {
		t.Fatal("LocationsFor(codex) reported an unknown harness")
	}
	existing := make(map[string]bool)
	for _, location := range locations {
		if location.Harness != registry.Harness("codex") {
			t.Errorf("location %+v is labeled with the wrong harness", location)
		}
		if location.Exists {
			existing[location.Path] = true
		}
	}
	want := map[string]bool{
		filepath.Join(home, ".codex", "config.toml"): true,
		filepath.Join(projectDir, "AGENTS.md"):       true,
	}
	if len(existing) != len(want) {
		t.Fatalf("existing paths = %v, want %v", existing, want)
	}
	for path := range want {
		if !existing[path] {
			t.Errorf("%s not reported as existing; existing = %v", path, existing)
		}
	}
}

func TestAllLocationsCoversEverySupportedHarness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	covered := make(map[registry.Harness]bool)
	for _, location := range harness.AllLocations(t.TempDir()) {
		covered[location.Harness] = true
	}
	for _, id := range harness.Supported() {
		if !covered[id] {
			t.Errorf("AllLocations has no entry for %s", id)
		}
	}
}

func TestLocationsForUnknownHarness(t *testing.T) {
	locations, ok := harness.LocationsFor(registry.Harness("no-such-harness"), t.TempDir())
	if ok || locations != nil {
		t.Fatalf("LocationsFor(unknown) = %v, %t, want nil, false", locations, ok)
	}
}
