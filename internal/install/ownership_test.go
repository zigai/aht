package install

import "testing"

func TestClassifyArtifactContentAcceptsSourceMetadata(t *testing.T) {
	t.Parallel()

	current := `{"command":"aht report codex --attribute aht_integration_version=8 --attribute aht_integration=codex-hook"}`
	if status := classifyArtifactContent(current); status != ArtifactCurrent {
		t.Fatalf("current source metadata classified as %q", status)
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
	current := "aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=8"
	if status := classifyArtifactContent(current); status != ArtifactCurrent {
		t.Fatalf("current agy status = %q", status)
	}
	stale := "aht managed integration\nAHT_INTEGRATION_ID=agy\nAHT_INTEGRATION_VERSION=7"
	if status := classifyArtifactContent(stale); status != ArtifactStale {
		t.Fatalf("stale agy status = %q", status)
	}
}
