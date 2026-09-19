package harness_test

import (
	"context"
	"encoding/json"
	"testing"

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
	t.Parallel()
	status, err := harness.InspectRuntime(context.Background(), registry.HarnessPi, "/path/to/fake-aht")
	if err != nil {
		t.Fatalf("unexpected error inspecting runtime: %v", err)
	}
	if status.Harness != registry.HarnessPi {
		t.Fatalf("status.Harness = %q, want %q", status.Harness, registry.HarnessPi)
	}
	// Runtime status reflects installed/current state, not static capabilities
	if status.Status == "" {
		t.Fatal("expected non-empty Status")
	}

	all := harness.AllRuntimeStatuses(context.Background(), "/path/to/fake-aht")
	if len(all) == 0 {
		t.Fatal("expected at least one runtime status")
	}
}
