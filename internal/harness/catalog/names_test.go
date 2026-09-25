package catalog_test

import (
	"slices"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestHarnessIsValid(t *testing.T) {
	t.Parallel()

	all := catalog.Harnesses()
	if len(all) == 0 {
		t.Fatal("AllHarnesses() returned empty slice")
	}

	for _, h := range all {
		if !(catalog.Rules{}).Known(h) {
			t.Errorf("expected harness %q to be valid", h)
		}
	}

	invalid := []registry.Harness{"", "unknown", "claude-code", "random"}
	for _, h := range invalid {
		if (catalog.Rules{}).Known(h) {
			t.Errorf("expected harness %q to be invalid", h)
		}
	}
}

func TestAllHarnessesCloned(t *testing.T) {
	t.Parallel()

	first := catalog.Harnesses()
	second := catalog.Harnesses()

	if !slices.Equal(first, second) {
		t.Fatalf("expected identical slices, got %v and %v", first, second)
	}

	first[0] = "mutated"
	if slices.Equal(first, catalog.Harnesses()) {
		t.Fatal("AllHarnesses did not return an isolated clone")
	}
}
