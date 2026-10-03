package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestIndexScopesFileMetadataAndCandidates(t *testing.T) {
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
		files   int
	}{
		{"agent", catalog.Sources, Query{Text: "needle", Harness: registry.Harness("pi")}, 2},
		{"file", []Source{{Harness: registry.Harness("pi"), Path: filepath.Join(root, "0.jsonl")}}, Query{Text: "needle"}, 1},
		{"ignored", catalog.Sources, Query{Text: "needle", IgnoreHarnesses: []registry.Harness{registry.Harness("pi")}}, 2},
		{"none", catalog.Sources, Query{Text: "needle", Harness: registry.Harness("codex")}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := Catalog{Sources: test.sources, IndexPath: catalog.IndexPath}
			var search search
			sources, err := selected.begin(t.Context(), test.query, &search)
			if err != nil {
				t.Fatal(err)
			}
			index, err := openHistoryIndex(t.Context(), selected.IndexPath, search.indexSources(sources))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := index.close(); err != nil {
					t.Error(err)
				}
			})
			if err := index.prepareQuery(t.Context(), &search); err != nil {
				t.Fatal(err)
			}
			if len(index.files) != test.files || len(index.candidates) != test.files {
				t.Fatalf("scope: files=%d candidates=%d want=%d", len(index.files), len(index.candidates), test.files)
			}
			compareIndexedSearch(t, selected, test.query)
		})
	}
}

func TestLargeSingleConversationUsesTrigramCandidates(t *testing.T) {
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
	var s search
	sources, err := c.begin(t.Context(), query, &s)
	if err != nil {
		t.Fatal(err)
	}
	index, err := openHistoryIndex(t.Context(), c.IndexPath, s.indexSources(sources))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = index.close() }()
	for _, fileID := range []int64{0, index.files[string(sources[0].Harness)+"\x00"+path].id} {
		statement, _, err := index.partQuery(t.Context(), &s, fileID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement, "parts_fts MATCH") {
			t.Fatalf("file %d: query scans every part instead of the trigram index: %s", fileID, statement)
		}
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
