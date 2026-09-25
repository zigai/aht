package aht_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/aht"
	"github.com/zigai/aht/v2/pkg/registry"
)

func writeTitleFixture(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookupTitlesReadsHarnessNativeNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	writeTitleFixture(t, codexHome, "session_index.jsonl", `{"id":"codex-1","thread_name":"Old title"}
{"id":"codex-2","thread_name":"Second thread"}
{"id":"codex-1","thread_name":"Current title"}
`)
	pi := writeTitleFixture(t, root, "pi.jsonl", `{"type":"session","id":"pi-1"}
{"type":"session_info","name":"First name"}
{"type":"message","message":{"content":"private prompt"}}
{"type":"session_info","name":"Current name"}
`)
	omp := writeTitleFixture(t, root, "omp.jsonl", `{"type":"title","title":"Fast title","pad":" "}
{"type":"session","id":"omp-1","title":"Stale header title"}
{"type":"message","message":{"content":"private prompt"}}
`)
	sessions := []aht.Session{
		{Harness: aht.HarnessCodex, SessionID: "codex-1", SessionPath: filepath.Join(codexHome, "sessions", "2026", "rollout.jsonl")},
		{Harness: aht.HarnessCodex, SessionID: "codex-2", SessionPath: filepath.Join(codexHome, "archived_sessions", "rollout.jsonl")},
		{Harness: aht.HarnessPi, SessionID: "pi-1", SessionPath: pi},
		{Harness: aht.HarnessOmp, SessionID: "omp-1", SessionPath: omp},
		{Harness: aht.HarnessPi, SessionID: "another-pi", SessionPath: pi},
		{Harness: aht.HarnessOmp, SessionID: "another-omp", SessionPath: omp},
		{Harness: aht.HarnessCodex, SessionID: "unknown", SessionPath: filepath.Join(codexHome, "sessions", "unknown.jsonl")},
		{Harness: aht.HarnessClaude, SessionID: "unsupported", SessionPath: pi},
		{Harness: aht.HarnessAmp, SessionID: "T-amp", Observations: registry.Observations{Native: &registry.NativeObservation{Attributes: map[string]string{"amp_title": "Amp title"}}}},
	}
	titles, err := aht.LookupTitles(t.Context(), sessions)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Current title", "Second thread", "Current name", "Fast title", "", "", "", "", "Amp title"}
	if diff := cmp.Diff(want, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestLookupTitlesPreservesSuccessfulNamesOnReadFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pi := writeTitleFixture(t, root, "pi.jsonl", `{"type":"session","id":"pi"}
{"type":"session_info","name":"Present"}
`)
	bad := writeTitleFixture(t, root, "bad.jsonl", strings.Repeat("x", 65<<10))
	titles, err := aht.LookupTitles(t.Context(), []aht.Session{
		{Harness: aht.HarnessPi, SessionID: "pi", SessionPath: pi},
		{Harness: aht.HarnessOmp, SessionID: "omp", SessionPath: bad},
	})
	if err == nil {
		t.Fatal("oversized native record did not report an error")
	}
	if diff := cmp.Diff([]string{"Present", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestLookupTitlesHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := aht.LookupTitles(ctx, []aht.Session{{Harness: aht.HarnessPi, SessionID: "pi", SessionPath: "ignored"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup error = %v", err)
	}
}

func TestLookupTitlesSupportIsDiscoverableThroughAHT(t *testing.T) {
	t.Parallel()
	for _, id := range []aht.Harness{
		aht.HarnessCodex, aht.HarnessPi, aht.HarnessOmp,
		aht.HarnessCline, aht.HarnessKimiCode, aht.HarnessGrok,
		aht.HarnessGoose, aht.HarnessAmp, aht.HarnessOpenCode,
		aht.HarnessKilo, aht.HarnessDroid, aht.HarnessOpenClaw,
		aht.HarnessHermes,
	} {
		capabilities, ok := aht.Capabilities(id)
		if !ok || !capabilities.TitleLookup {
			t.Fatalf("%s title lookup capability = %t, %t", id, capabilities.TitleLookup, ok)
		}
	}
}
