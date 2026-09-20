package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/pkg/registry"
)

func TestIndexMatchesDirectScan(t *testing.T) {
	t.Parallel()
	c := parityFixture(t)
	//nolint:gosmopolitan // Exercise literal matching of multi-byte Unicode queries.
	for _, text := range []string{"needle", "NEEDLE", "istanbul", "你好", "你好世", "%b_", `"quoted"`, "AND OR", "a", "é", "after-token", "\x00after", "not-present"} {
		for _, dir := range []string{"", "/work/a", "/"} {
			for _, sensitive := range []bool{false, true} {
				q := Query{Text: text, Dir: dir, CaseSensitive: sensitive, Limit: 1}
				compareIndexedSearch(t, c, q)
			}
		}
	}
}

func parityFixture(t *testing.T) Catalog {
	t.Helper()
	root := t.TempDir()
	//nolint:gosmopolitan,dupword // Intentional Unicode and repeated-token matching fixtures.
	texts := []string{"İSTANBUL NEEDLE élan 你好世界", `a%b_c *?[ ] "quoted" AND OR NEAR`, "before\x00after-token", strings.Repeat("é", 300) + "needle", "needle needle"}
	for i := range 2 {
		cwd := "/work/a"
		if i == 1 {
			cwd = "/work/ab"
		}
		var body strings.Builder
		fmt.Fprintf(&body, "{\"type\":\"session\",\"id\":\"s%d\",\"cwd\":%q}\n", i, cwd)
		for n, text := range texts {
			data, err := json.Marshal(map[string]any{"type": "message", "id": strconv.Itoa(n), "message": map[string]any{"role": "user", "content": text}})
			if err != nil {
				t.Fatal(err)
			}
			body.WriteString(string(data) + "\n")
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%d.jsonl", i)), []byte(body.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Catalog{Sources: []Source{{Harness: registry.HarnessPi, Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
}

// compareIndexedSearch checks the index against the production direct scan.
func compareIndexedSearch(t *testing.T, c Catalog, q Query) {
	t.Helper()
	want, wantErr := c.searchDirect(t.Context(), q)
	got, err := c.Search(t.Context(), q)
	if errors.Is(err, ErrIncomplete) != errors.Is(wantErr, ErrIncomplete) {
		t.Fatalf("query %#v errors: %v / %v", q, err, wantErr)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("query %#v (-direct +index):\n%s", q, diff)
	}
}

func BenchmarkHistorySearch(b *testing.B) {
	c := benchmarkCatalog(b)
	queries := []struct {
		name    string
		q       Query
		matches int
	}{
		{"all", Query{Text: "needle"}, 1000},
		{"directory", Query{Text: "needle", Dir: "/work/selected"}, 10},
		{"missing", Query{Text: "absent-token", Dir: "/work/selected"}, 0},
	}
	for _, query := range queries {
		for name, run := range map[string]func(context.Context, Query) (Result, error){
			"direct":  c.searchDirect,
			"indexed": c.Search,
		} {
			b.Run(query.name+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					result, err := run(b.Context(), query.q)
					if err != nil || len(result.Matches) != query.matches {
						b.Fatalf("search = %d matches, %v", len(result.Matches), err)
					}
				}
			})
		}
	}
}

func benchmarkCatalog(b *testing.B) Catalog {
	b.Helper()
	root := b.TempDir()
	message, err := json.Marshal(map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": strings.Repeat("Example retained conversation content. ", 50) + "needle"}})
	if err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		cwd := "/work/other"
		if i%100 == 0 {
			cwd = "/work/selected"
		}
		body := fmt.Sprintf("{\"type\":\"session\",\"id\":\"s%d\",\"cwd\":%q}\n", i, cwd) + strings.Repeat(string(message)+"\n", 20)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%04d.jsonl", i)), []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	c := Catalog{Sources: []Source{{Harness: registry.HarnessPi, Path: root}}, IndexPath: filepath.Join(b.TempDir(), "index.sqlite")}
	if _, err := c.Search(b.Context(), Query{Text: "needle"}); err != nil {
		b.Fatal(err)
	}
	return c
}

func TestIndexCanceledRefreshRollsBack(t *testing.T) {
	t.Parallel()
	c := parityFixture(t)
	if _, err := c.Search(t.Context(), Query{Text: "needle"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	index, err := openHistoryIndex(ctx, c.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(); err != nil {
			t.Error(err)
		}
	}()
	source := c.Sources[0]
	path := filepath.Join(source.Path, "0.jsonl")
	file := index.files[string(source.Harness)+"\x00"+path]
	err = index.refresh(ctx, new(search), source, path, "changed", &file, func(writer *indexWriter, _ *search, _ indexedFile) error {
		writer.append(ctx, Excerpt{Role: "user", Text: "canceled token"})
		writer.finish(ctx, Conversation{Harness: source.Harness, Path: path, SessionID: "uncommitted"})
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh = %v", err)
	}
	if err := index.close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", c.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var stored int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM conversations WHERE instr(metadata,'uncommitted')>0").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("canceled refresh stored %d conversations, %v", stored, err)
	}
	compareIndexedSearch(t, c, Query{Text: "needle"})
	compareIndexedSearch(t, c, Query{Text: "canceled token"})
}
