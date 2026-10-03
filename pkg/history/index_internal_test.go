package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDirectScanChecksEscapedTextAndMalformedRecords(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	body := `{"type":"session","id":"s","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"starter"}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"\u006eeedle"}}` + "\n" +
		"{broken\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	query := Query{Text: "needle"}
	got, err := c.searchDirect(t.Context(), query)
	if !errors.Is(err, ErrIncomplete) || len(got.Matches) != 1 || len(got.Issues) != 1 {
		t.Fatalf("direct search: matches=%d issues=%d err=%v", len(got.Matches), len(got.Issues), err)
	}
	compareIndexedSearch(t, c, query)
}

func TestNativeFastRecordsKeepSearchMetadataAndInvalidLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, filename, body, updated string
		harness                       registry.Harness
		tools                         int
	}{
		{"pi", "session.jsonl", `{"type":"session","id":"s","cwd":"/work","timestamp":"2026-09-01T10:00:00Z"}
{"type":"message","id":"u","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":"needle"}}
{"type":"message","id":"t","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":[{"type":"text","text":"needle"}]}}
{"type":"compaction","timestamp":"2026-09-01T10:04:00Z","summary":"needle"}
{"type":"compaction","summary":
`, "2026-09-01T10:03:00Z", registry.Harness("pi"), 2},
		{"omp", "session.jsonl", `{"type":"session","id":"s","cwd":"/work","timestamp":"2026-09-01T10:00:00Z"}
{"type":"message","id":"u","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":"needle"}}
{"type":"message","id":"t","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":[{"type":"text","text":"needle"}]}}
{"type":"custom","timestamp":"2026-09-01T10:04:00Z","data":{"needle":"needle"}}
{"type":"custom","data":
`, "2026-09-01T10:03:00Z", registry.Harness("omp"), 2},
		{"codex", "rollout-s.jsonl", `{"type":"session_meta","payload":{"id":"s","cwd":"/work","timestamp":"2026-09-01T10:00:00Z"}}
{"type":"response_item","timestamp":"2026-09-01T10:01:00Z","payload":{"type":"message","role":"user","content":"needle"}}
{"type":"response_item","timestamp":"2026-09-01T10:03:00Z","payload":{"type":"function_call_output","call_id":"t","output":"needle"}}
{"type":"response_item","timestamp":"2026-09-01T10:04:00Z","payload":{"type":"message","channel":"analysis","role":"assistant","content":"needle"}}
{"type":"event_msg","timestamp":"2026-09-01T10:05:00Z","payload":{"message":"needle"}}
{"type":"event_msg","payload":
`, "2026-09-01T10:03:00Z", registry.Harness("codex"), 2},
		{"claude", "session.jsonl", `{"type":"user","sessionId":"s","cwd":"/work","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":"needle"}}
{"type":"assistant","sessionId":"s","timestamp":"2026-09-01T10:02:00Z","message":{"role":"assistant","content":"reply"}}
{"type":"progress","timestamp":"2026-09-01T10:04:00Z","data":{"needle":"needle"}}
{"type":"progress","data":
`, "2026-09-01T10:02:00Z", registry.Harness("claude"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			c := Catalog{Sources: []Source{{Harness: tt.harness, Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			for _, tools := range []bool{false, true} {
				q := Query{Text: "needle", IncludeTools: tools}
				got, err := c.searchDirect(t.Context(), q)
				wantParts := 1
				if tools {
					wantParts = tt.tools
				}
				if !errors.Is(err, ErrIncomplete) || len(got.Issues) != 1 || len(got.Matches) != 1 || got.Matches[0].MatchingParts != wantParts || got.Matches[0].Conversation.UpdatedAt.Format(time.RFC3339) != tt.updated {
					t.Fatalf("tools=%t: matches=%#v issues=%#v err=%v", tools, got.Matches, got.Issues, err)
				}
				compareIndexedSearch(t, c, q)
			}
		})
	}
}

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
	return Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
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
		{"limited", Query{Text: "needle", Limit: 10}, 10},
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
	c := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(b.TempDir(), "index.sqlite")}
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
	index, err := openHistoryIndex(ctx, c.IndexPath, nil)
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

func TestTwoLoadedIndexesDoNotDuplicateAppend(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	body := "{\"type\":\"session\",\"id\":\"review-session\",\"cwd\":\"/work\"}\n" +
		"{\"type\":\"message\",\"id\":\"seed\",\"message\":{\"role\":\"user\",\"content\":\"seed token\"}}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sources := []Source{{Harness: registry.Harness("pi"), Path: root}}
	indexPath := filepath.Join(t.TempDir(), "index.sqlite")
	catalog := Catalog{Sources: sources, IndexPath: indexPath}
	if _, err := catalog.Search(ctx, Query{Text: "seed token"}); err != nil {
		t.Fatal(err)
	}

	first, second := openTwoTestIndexes(t, ctx, indexPath, sources)
	defer func() { _ = first.close() }()
	defer func() { _ = second.close() }()

	appendTranscriptLine(t, path, "append", "review appended token")
	indexTranscriptFile(t, ctx, first, sources[0], path, "review appended token")
	indexTranscriptFile(t, ctx, second, sources[0], path, "review appended token")

	var count int
	if err := second.conn.QueryRowContext(ctx, "SELECT count(*) FROM parts WHERE message_id='append'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("append indexed %d times; want exactly once", count)
	}
}

func openTwoTestIndexes(t *testing.T, ctx context.Context, indexPath string, sources []Source) (*historyIndex, *historyIndex) {
	t.Helper()
	first, err := openHistoryIndex(ctx, indexPath, sources)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openHistoryIndex(ctx, indexPath, sources)
	if err != nil {
		_ = first.close()
		t.Fatal(err)
	}
	return first, second
}

func appendTranscriptLine(t *testing.T, path, id, token string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	msg := "{\"type\":\"message\",\"id\":\"" + id + "\",\"message\":{\"role\":\"user\",\"content\":\"" + token + "\"}}\n"
	if _, err := file.WriteString(msg); err != nil {
		t.Fatal(err)
	}
}

func indexTranscriptFile(t *testing.T, ctx context.Context, index *historyIndex, source Source, path, needle string) {
	t.Helper()
	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	s := &search{index: index, query: Query{Text: needle, Limit: 100}, needle: needle}
	if err := index.transcript(ctx, s, source, path, opened); err != nil {
		t.Fatal(err)
	}
}

func TestForeignDatabaseNotAltered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	foreignPath := filepath.Join(t.TempDir(), "foreign.sqlite")
	beforeHash := createForeignTestDB(t, ctx, foreignPath)

	sources := []Source{{Harness: registry.Harness("pi"), Path: t.TempDir()}}
	idx, err := openHistoryIndex(ctx, foreignPath, sources)
	if idx != nil {
		defer func() { _ = idx.close() }()
	}
	if err == nil {
		t.Fatal("expected error opening foreign database")
	}

	verifyForeignDBUnmodified(t, ctx, foreignPath, beforeHash)
}

func createForeignTestDB(t *testing.T, ctx context.Context, foreignPath string) [32]byte {
	t.Helper()
	db, err := sql.Open("sqlite", foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE; CREATE TABLE unrelated_data(value TEXT); INSERT INTO unrelated_data VALUES('keep me'); PRAGMA user_version=2;"); err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(beforeBytes)
}

func verifyForeignDBUnmodified(t *testing.T, ctx context.Context, foreignPath string, beforeHash [32]byte) {
	t.Helper()
	verifyDB, err := sql.Open("sqlite", foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifyDB.Close() }()
	var mode string
	if err := verifyDB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Fatalf("journal mode was mutated to %q, want delete", mode)
	}
	var val string
	if err := verifyDB.QueryRowContext(ctx, "SELECT value FROM unrelated_data").Scan(&val); err != nil {
		t.Fatal(err)
	}
	if val != "keep me" {
		t.Fatalf("data changed: %q", val)
	}

	afterBytes, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	afterHash := sha256.Sum256(afterBytes)
	if beforeHash != afterHash {
		t.Fatalf("foreign database bytes changed from %s to %s", hex.EncodeToString(beforeHash[:]), hex.EncodeToString(afterHash[:]))
	}
}
