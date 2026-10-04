package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestScopedIndexedSearchMatchesDirectSearch(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	root := catalog.Sources[0].Path
	catalog.Sources = append(catalog.Sources, Source{Harness: registry.Harness("omp"), Path: root})
	if _, err := catalog.Search(t.Context(), Query{Text: "needle"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		sources []Source
		query   Query
	}{
		{"agent", catalog.Sources, Query{Text: "needle", Harness: registry.Harness("pi")}},
		{"file", []Source{{Harness: registry.Harness("pi"), Path: filepath.Join(root, "0.jsonl")}}, Query{Text: "needle"}},
		{"ignored", catalog.Sources, Query{Text: "needle", IgnoreHarnesses: []registry.Harness{registry.Harness("pi")}}},
		{"none", catalog.Sources, Query{Text: "needle", Harness: registry.Harness("codex")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compareIndexedSearch(t, Catalog{Sources: test.sources, IndexPath: catalog.IndexPath}, test.query)
		})
	}
}

func TestLargeSingleConversationIndexedSearchMatchesDirectSearch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	body := `{"type":"session","id":"large","cwd":"/work"}` + "\n" +
		strings.Repeat(`{"type":"message","message":{"role":"user","content":"ordinary text"}}`+"\n", 40) +
		`{"type":"message","message":{"role":"user","content":"rare target phrase"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	query := Query{Text: "rare target phrase", Limit: 1}
	if result, err := c.Search(t.Context(), query); err != nil || len(result.Matches) != 1 {
		t.Fatalf("index setup: %d matches, %v", len(result.Matches), err)
	}
	compareIndexedSearch(t, c, query)
}

func BenchmarkHistorySourceSearch(b *testing.B) {
	catalog := benchmarkCatalog(b)
	catalog.Sources[0].Path = filepath.Join(catalog.Sources[0].Path, "0000.jsonl")
	b.ReportAllocs()
	for b.Loop() {
		result, err := catalog.Search(b.Context(), Query{Text: "needle"})
		if err != nil || len(result.Matches) != 1 {
			b.Fatalf("search = %d matches, %v", len(result.Matches), err)
		}
	}
}
