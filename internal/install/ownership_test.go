package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestClassifyArtifactContentAcceptsSourceMetadata(t *testing.T) {
	t.Parallel()

	codex := registry.Harness("codex")
	version := adapters.IntegrationVersionFor(codex)

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
	version := adapters.IntegrationVersionFor(agy)

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

func TestClassifyArtifactContentRequiresHarnessSource(t *testing.T) {
	t.Parallel()

	codex := registry.Harness("codex")
	version := adapters.IntegrationVersionFor(codex)

	for name, content := range map[string]string{
		"unrelated reporter": `{"hooks":{"Stop":[{"hooks":[{"command":"jest --reporter spec"}]}]}}`,
		"other harness": fmt.Sprintf(
			`{"command":"aht report claude --reporter-version %d --reporter claude-hook"}`,
			version,
		),
		"other legacy harness": `{"command":"aht report claude --attribute ` +
			`aht_integration_version=7 --attribute aht_integration=claude-hook"}`,
		"other managed block": "aht managed integration\nAHT_INTEGRATION_ID=kimi\n" +
			"AHT_INTEGRATION_VERSION=1\n",
	} {
		if status := classifyArtifactContent(content, codex); status != ArtifactForeign {
			t.Errorf("%s classified for codex as %q", name, status)
		}
	}

	mixed := fmt.Sprintf(
		`{"Stop":[{"command":"jest --reporter-version 1 --reporter spec"},`+
			`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}]}`,
		version,
	)
	if status := classifyArtifactContent(mixed, codex); status != ArtifactCurrent {
		t.Fatalf("current codex hook beside a user reporter classified as %q", status)
	}

	partial := fmt.Sprintf(
		`{"Stop":[{"command":"aht report codex --reporter-version %d --reporter codex-hook"},`+
			`{"command":"aht report codex --reporter-version %d --reporter codex-hook"}]}`,
		version,
		version-1,
	)
	if status := classifyArtifactContent(partial, codex); status != ArtifactStale {
		t.Fatalf("partially upgraded codex hooks classified as %q", status)
	}
}

func TestInspectSharedFileIgnoresForeignReporters(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hooks.json")
	content := `{"hooks":{"Stop":[{"hooks":[` +
		`{"type":"command","command":"jest --reporter spec"},` +
		`{"type":"command","command":"aht report claude --reporter-version 3 --reporter claude-hook"}` +
		`]}]}}`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	statuses, err := inspectSharedFile(path, registry.Harness("codex"))
	if err != nil {
		t.Fatal(err)
	}

	if len(statuses) != 1 || statuses[0].status != ArtifactMissing {
		t.Fatalf("shared file with foreign reporters = %+v", statuses)
	}
}

func TestClassifyArtifactRequiresManagedMarkerForOwnedFiles(t *testing.T) {
	t.Parallel()

	pi := registry.Harness("pi")
	version := adapters.IntegrationVersionFor(pi)
	dir := t.TempDir()

	for name, content := range map[string]string{
		"user reporter":   "#!/bin/sh\nexec aht report session-start --reporter mine\n",
		"test runner":     "jest --reporter spec\n",
		"unmarked source": fmt.Sprintf("aht report pi --reporter-version %d --reporter pi-extension\n", version),
		"marked foreign source": fmt.Sprintf(
			"// aht managed integration\naht report claude --reporter-version %d --reporter claude-hook\n",
			version,
		),
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		status, err := classifyArtifact(path, pi)
		if err != nil {
			t.Fatal(err)
		}

		if status != ArtifactForeign {
			t.Errorf("%s classified for pi as %q", name, status)
		}
	}
}
