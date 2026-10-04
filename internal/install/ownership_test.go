package install

import (
	"fmt"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestClassifyArtifactContentAcceptsSourceMetadata(t *testing.T) {
	t.Parallel()

	current := fmt.Sprintf(`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}`, harness.IntegrationVersion)
	if status := classifyArtifactContent(current); status != ArtifactCurrent {
		t.Fatalf("current source metadata classified as %q", status)
	}
	previous := fmt.Sprintf(`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}`, harness.IntegrationVersion-1)
	if status := classifyArtifactContent(previous); status != ArtifactStale {
		t.Fatalf("previous source metadata classified as %q", status)
	}

	stale := `{"command":"aht report codex --attribute aht_integration_version=7 --attribute aht_integration=codex-hook"}`
	if status := classifyArtifactContent(stale); status != ArtifactStale {
		t.Fatalf("stale source metadata classified as %q", status)
	}

	foreign := `{"hooks":{"Stop":[{"command":"custom-tool"}]}}`
	if status := classifyArtifactContent(foreign); status != ArtifactForeign {
		t.Fatalf("foreign content classified as %q", status)
	}
}

func TestClassifyArtifactContentUsesHarnessGeneration(t *testing.T) {
	t.Parallel()
	version := catalog.IntegrationVersionFor(registry.Harness("agy"))
	current := fmt.Sprintf("aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=%d", version)
	if status := classifyArtifactContent(current); status != ArtifactCurrent {
		t.Fatalf("current agy status = %q", status)
	}
	stale := fmt.Sprintf("aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=%d", version-1)
	if status := classifyArtifactContent(stale); status != ArtifactStale {
		t.Fatalf("stale agy status = %q", status)
	}
}
