package install

import (
	"slices"
	"testing"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

func TestInstallPlansMatchHarnessCatalog(t *testing.T) {
	t.Parallel()

	for _, adapter := range harnesscatalog.All() {
		if _, ok := adapter.(harnesspkg.Installable); !ok {
			t.Fatalf("harness %q has no install plan", adapter.Definition().ID)
		}
	}

	for _, harness := range AllHarnesses() {
		adapter, ok := harnesscatalog.Find(harness)
		if !ok {
			t.Fatalf("AllHarnesses contains unknown harness %q", harness)
		}
		if _, installable := adapter.(harnesspkg.Installable); !installable {
			t.Fatalf("AllHarnesses contains %q without install plan", harness)
		}
	}
}

func TestAllHarnesses(t *testing.T) {
	t.Parallel()

	harnesses := AllHarnesses()
	if len(harnesses) == 0 {
		t.Fatal("expected installable harnesses")
	}
	if !slices.Contains(harnesses, registry.HarnessCodex) {
		t.Fatalf("AllHarnesses() = %v, want codex", harnesses)
	}
}
