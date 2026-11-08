package harness_test

import (
	"encoding/json"
	"slices"
	"testing"

	internalharness "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
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

func TestCapabilitiesReportNativeTitleReaders(t *testing.T) {
	t.Parallel()

	for _, adapter := range catalog.All() {
		id := adapter.Definition().ID
		_, titleReader := adapter.(internalharness.TitleReader)

		caps, ok := harness.CapabilitiesFor(id)
		if !ok || caps.TitleLookup != titleReader {
			t.Fatalf(
				"%s title lookup capability = %t, %t; reader available = %t",
				id,
				caps.TitleLookup,
				ok,
				titleReader,
			)
		}
	}
}

func TestAllCapabilities(t *testing.T) {
	t.Parallel()

	all := harness.AllCapabilities()
	supported := catalog.Harnesses()

	if len(all) != len(supported) {
		t.Fatalf("len(AllCapabilities) = %d, len(Supported) = %d", len(all), len(supported))
	}

	seen := make(map[registry.Harness]bool)

	for _, c := range all {
		if !slices.Contains(supported, c.Harness) || seen[c.Harness] {
			t.Errorf("unexpected or duplicate capabilities for %s", c.Harness)
		}

		seen[c.Harness] = true

		if c.Authority == "" {
			t.Errorf("harness %s has empty Authority", c.Harness)
		}
	}
}

func TestCapabilitiesJSONCompatibility(t *testing.T) {
	t.Parallel()

	caps, ok := harness.CapabilitiesFor(registry.Harness("pi"))
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
		"process_identity", "native_catalog", "installable",
		"resumable", "title_lookup", "screen_support", "screen_fallback", "authority",
	}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing expected field %q in capabilities JSON", field)
		}
	}
}
