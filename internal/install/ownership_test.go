package install

import (
	"fmt"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestClassifyArtifactContentAcceptsSourceMetadata(t *testing.T) {
	t.Parallel()

	codex := registry.Harness("codex")
	version := catalog.IntegrationVersionFor(codex)

	current := fmt.Sprintf(
		`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}`,
		version,
	)
	if status := classifyArtifactContent(current, codex); status != ArtifactCurrent {
		t.Fatalf("current source metadata classified as %q", status)
	}

	previous := fmt.Sprintf(
		`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}`,
		version-1,
	)
	if status := classifyArtifactContent(previous, codex); status != ArtifactStale {
		t.Fatalf("previous source metadata classified as %q", status)
	}

	stale := `{"command":"aht report codex --attribute ` +
		`aht_integration_version=7 --attribute ` +
		`aht_integration=codex-hook"}`
	if status := classifyArtifactContent(stale, codex); status != ArtifactStale {
		t.Fatalf("stale source metadata classified as %q", status)
	}

	foreign := `{"hooks":{"Stop":[{"command":"custom-tool"}]}}`
	if status := classifyArtifactContent(foreign, codex); status != ArtifactForeign {
		t.Fatalf("foreign content classified as %q", status)
	}
}

func TestClassifyArtifactContentUsesHarnessGeneration(t *testing.T) {
	t.Parallel()

	agy := registry.Harness("agy")
	version := catalog.IntegrationVersionFor(agy)

	current := fmt.Sprintf(
		"aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=%d",
		version,
	)
	if status := classifyArtifactContent(current, agy); status != ArtifactCurrent {
		t.Fatalf("current agy status = %q", status)
	}

	stale := fmt.Sprintf(
		"aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=%d",
		version-1,
	)
	if status := classifyArtifactContent(stale, agy); status != ArtifactStale {
		t.Fatalf("stale agy status = %q", status)
	}

	if status := classifyArtifactContent(
		current,
		registry.Harness("goose"),
	); status != ArtifactForeign {
		t.Fatalf("agy artifact classified for goose as %q", status)
	}
}
