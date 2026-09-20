package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/registry"
)

func TestCapabilitiesForUnsupported(t *testing.T) {
	t.Parallel()
	caps, ok := harness.CapabilitiesFor("nonexistent-harness")
	if ok {
		t.Fatal("CapabilitiesFor should return false for nonexistent harness")
	}
	if caps.Harness != "" {
		t.Fatalf("caps.Harness = %q, want empty", caps.Harness)
	}
}

func TestCapabilitiesForPi(t *testing.T) {
	t.Parallel()
	caps, ok := harness.CapabilitiesFor(registry.HarnessPi)
	if !ok {
		t.Fatal("CapabilitiesFor(HarnessPi) returned false")
	}
	if caps.Harness != registry.HarnessPi {
		t.Fatalf("Harness = %q, want %q", caps.Harness, registry.HarnessPi)
	}
	if caps.Authority != "hook" {
		t.Fatalf("Authority = %q, want hook", caps.Authority)
	}
	if !caps.Installable {
		t.Fatal("Pi should be installable")
	}
	if !caps.Resumable {
		t.Fatal("Pi should be resumable")
	}
	if !caps.ScreenFallback {
		t.Fatal("Pi should support screen fallback")
	}
	if !caps.SessionStart || !caps.SessionEnd || !caps.RunningIdle {
		t.Fatalf("expected Pi to support lifecycle events: %+v", caps)
	}
}

func TestCapabilitiesForCodex(t *testing.T) {
	t.Parallel()
	caps, ok := harness.CapabilitiesFor(registry.HarnessCodex)
	if !ok {
		t.Fatal("CapabilitiesFor(HarnessCodex) returned false")
	}
	if caps.Authority != "screen" {
		t.Fatalf("Codex Authority = %q, want screen", caps.Authority)
	}
	if !caps.ScreenSupport {
		t.Fatal("Codex should have screen support")
	}
}

func TestAllCapabilities(t *testing.T) {
	t.Parallel()
	all := harness.AllCapabilities()
	supported := harness.Supported()

	if len(all) != len(supported) {
		t.Fatalf("len(AllCapabilities) = %d, len(Supported) = %d", len(all), len(supported))
	}

	foundPi := false
	foundCodex := false
	for _, c := range all {
		if c.Harness == registry.HarnessPi {
			foundPi = true
		}
		if c.Harness == registry.HarnessCodex {
			foundCodex = true
		}
		if c.Authority == "" {
			t.Errorf("harness %s has empty Authority", c.Harness)
		}
	}
	if !foundPi || !foundCodex {
		t.Fatalf("missing expected harnesses in AllCapabilities: pi=%v, codex=%v", foundPi, foundCodex)
	}
}

func TestCapabilitiesJSONCompatibility(t *testing.T) {
	t.Parallel()
	caps, ok := harness.CapabilitiesFor(registry.HarnessPi)
	if !ok {
		t.Fatal("CapabilitiesFor failed")
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	expectedFields := []string{
		"harness", "session_start", "session_end", "running_idle", "waiting_permission",
		"process_identity", "native_catalog", "tty_tmux_context", "installable",
		"resumable", "screen_support", "screen_fallback", "authority",
	}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing expected field %q in capabilities JSON", field)
		}
	}
}

func TestInspectRuntimeSeparatesStaticFromRuntime(t *testing.T) {
	tempHome := t.TempDir()
	piDir := filepath.Join(tempHome, ".pi", "agent")
	stateDir := filepath.Join(tempHome, "state")
	t.Setenv("HOME", tempHome)
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	t.Setenv("AHT_STATE_DIR", stateDir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tempHome, ".local", "share"))

	ctx := context.Background()
	fakeBin := "/path/to/fake-aht"

	// 1. Missing state
	status, err := harness.InspectRuntime(ctx, registry.HarnessPi, fakeBin)
	if err != nil {
		t.Fatalf("unexpected error inspecting runtime: %v", err)
	}
	assertExpectedRuntimeStatus(t, status, registry.HarnessPi, false, false, "missing")

	// 2. Current state after installation
	if _, err := install.RunContext(ctx, install.Options{Harness: registry.HarnessPi, Binary: fakeBin, Force: true}); err != nil {
		t.Fatalf("installing pi extension: %v", err)
	}
	status, err = harness.InspectRuntime(ctx, registry.HarnessPi, fakeBin)
	if err != nil {
		t.Fatalf("unexpected error inspecting runtime after install: %v", err)
	}
	assertExpectedRuntimeStatus(t, status, registry.HarnessPi, true, true, "current")

	// 3. Stale state when extension version is older
	writeStalePiExtension(t, piDir)
	status, err = harness.InspectRuntime(ctx, registry.HarnessPi, fakeBin)
	if err != nil {
		t.Fatalf("unexpected error inspecting runtime after stale write: %v", err)
	}
	assertExpectedRuntimeStatus(t, status, registry.HarnessPi, true, false, "stale")

	// 4. Inspection error state for unsupported harness
	errStatus, err := harness.InspectRuntime(ctx, "nonexistent", fakeBin)
	if err == nil {
		t.Fatal("expected error for nonexistent harness, got nil")
	}
	assertExpectedRuntimeStatus(t, errStatus, "nonexistent", false, false, "error")

	// 5. Aggregate status check against known outcomes
	all := harness.AllRuntimeStatuses(ctx, fakeBin)
	if len(all) != len(harness.Supported()) {
		t.Fatalf("AllRuntimeStatuses count = %d, want %d", len(all), len(harness.Supported()))
	}
	assertAllRuntimeStatusesContainsPiStale(t, all)
}

func assertAllRuntimeStatusesContainsPiStale(t *testing.T, all []harness.RuntimeStatus) {
	t.Helper()
	foundPi := false
	for _, st := range all {
		if st.Harness == registry.HarnessPi {
			foundPi = true
			if !st.Installed || st.Current || st.Status != "stale" {
				t.Fatalf("AllRuntimeStatuses Pi entry = %#v, want stale", st)
			}
		}
		if st.Status == "" {
			t.Errorf("harness %s has empty status in AllRuntimeStatuses", st.Harness)
		}
	}
	if !foundPi {
		t.Fatal("Pi not found in AllRuntimeStatuses")
	}
}

func assertExpectedRuntimeStatus(t *testing.T, status harness.RuntimeStatus, id registry.Harness, installed, current bool, statusStr string) {
	t.Helper()
	if status.Harness != id || status.Installed != installed || status.Current != current || status.Status != statusStr {
		t.Fatalf("status = %#v, want harness=%s, installed=%v, current=%v, status=%s", status, id, installed, current, statusStr)
	}
}

func writeStalePiExtension(t *testing.T, piDir string) {
	t.Helper()
	extPath := filepath.Join(piDir, "extensions", "aht-state.ts")
	staleContent := "\"aht managed integration\";\n\"AHT_INTEGRATION_ID=pi\";\n\"AHT_INTEGRATION_VERSION=5\";\n"
	if err := os.WriteFile(extPath, []byte(staleContent), 0o600); err != nil {
		t.Fatalf("writing stale extension: %v", err)
	}
}
