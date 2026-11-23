package adapters_test

import (
	"slices"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestHarnessesAreKnown(t *testing.T) {
	t.Parallel()

	all := adapters.Harnesses()
	if len(all) == 0 {
		t.Fatal("Harnesses() returned empty slice")
	}

	for _, h := range all {
		if !(adapters.Rules{}).Known(h) {
			t.Errorf("expected harness %q to be valid", h)
		}
	}

	invalid := []registry.Harness{"", "unknown", "claude-code", "random"}
	for _, h := range invalid {
		if (adapters.Rules{}).Known(h) {
			t.Errorf("expected harness %q to be invalid", h)
		}
	}
}

func TestHarnessesCloned(t *testing.T) {
	t.Parallel()

	first := adapters.Harnesses()
	second := adapters.Harnesses()

	if !slices.Equal(first, second) {
		t.Fatalf("expected identical slices, got %v and %v", first, second)
	}

	first[0] = "mutated"
	if slices.Equal(first, adapters.Harnesses()) {
		t.Fatal("Harnesses did not return an isolated clone")
	}
}
