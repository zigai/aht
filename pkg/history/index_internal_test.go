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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"pgregory.net/rapid"

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
	query := Query{Terms: []string{"needle"}}
	got, err := c.searchDirect(t.Context(), query)
	if err != nil || len(got.Matches) != 1 || len(got.Issues) != 1 || !got.Issues[0].Record {
		t.Fatalf("direct search: matches=%d issues=%d err=%v", len(got.Matches), len(got.Issues), err)
	}
	compareIndexedSearch(t, c, query)
}

func malformedIssueFixture(t *testing.T) Catalog {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"recent", "old"} {
		timestamp := "2026-09-01T10:00:00Z"
		if name == "old" {
			timestamp = "2025-01-01T10:00:00Z"
		}
		body := fmt.Sprintf(`{"type":"session","id":%q,"cwd":"/work","timestamp":%q}`+"\n"+`{"type":"message","message":{"role":"user","content":"needle"}}`+"\n{broken\n", name, timestamp)
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
}

func TestIssuesFollowTextMatchesAndMetadata(t *testing.T) {
	t.Parallel()
	catalog := malformedIssueFixture(t)
	for _, test := range []struct {
		query Query
		count int
	}{
		{Query{Terms: []string{"needle"}, Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, 1},
		{Query{Terms: []string{"broken"}}, 0},
		{Query{Terms: []string{"needle", "broken"}}, 0},
		{Query{Terms: []string{"needle"}, Exclude: []string{"needle"}}, 0},
		{Query{Terms: []string{"needle"}, Role: "assistant"}, 0},
		{Query{Terms: []string{"needle"}, Presence: registry.PresenceLive}, 0},
	} {
		for _, tools := range []bool{false, true} {
			query := test.query
			query.IncludeTools = tools
			result, err := catalog.Search(t.Context(), query)
			if err != nil || len(result.Matches) != test.count || len(result.Issues) != test.count || result.OmittedIssues != 0 {
				t.Fatalf("query=%#v result=%#v err=%v", query, result, err)
			}
			for _, issue := range result.Issues {
				if filepath.Base(issue.Path) != "recent.jsonl" || !issue.Record {
					t.Fatalf("matching issue=%#v", issue)
				}
			}
			compareIndexedSearch(t, catalog, query)
		}
	}
}

func TestMetadataOnlyIssuesRemainScopedByMetadata(t *testing.T) {
	t.Parallel()
	catalog := malformedIssueFixture(t)
	listing, err := catalog.List(t.Context(), ListQuery{Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil || len(listing.Issues) != 1 || listing.OmittedIssues != 1 || filepath.Base(listing.Issues[0].Path) != "recent.jsonl" {
		t.Fatalf("listing=%#v err=%v", listing, err)
	}
	direct, err := catalog.listDirect(t.Context(), ListQuery{Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(direct, listing, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
		t.Fatalf("listing differs (-direct +indexed):\n%s", diff)
	}
}

func TestExplicitFileRecordIssuesRequireTextMatch(t *testing.T) {
	t.Parallel()
	catalog := malformedIssueFixture(t)
	catalog.Sources[0].Path = filepath.Join(catalog.Sources[0].Path, "old.jsonl")
	query := Query{Terms: []string{"needle"}, Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	result, err := catalog.Search(t.Context(), query)
	if err != nil || len(result.Issues) != 0 || result.OmittedIssues != 0 {
		t.Fatalf("explicit file result=%#v err=%v", result, err)
	}
	compareIndexedSearch(t, catalog, query)
}

func fileFailureFixture(t *testing.T, unreadable bool) Catalog {
	t.Helper()
	root := t.TempDir()
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, file := range []struct {
		name string
		at   time.Time
	}{{"old.jsonl", old}, {"recent.jsonl", recent}} {
		path := filepath.Join(root, file.name)
		if err := os.WriteFile(path, []byte("{\"unknown\":true}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, file.at, file.at); err != nil {
			t.Fatal(err)
		}
		if unreadable {
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	return Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
}

func TestFileFailuresAreScopedByModificationTime(t *testing.T) {
	t.Parallel()
	for _, unreadable := range []bool{false, true} {
		t.Run(strconv.FormatBool(unreadable), func(t *testing.T) {
			t.Parallel()
			catalog := fileFailureFixture(t, unreadable)
			if _, err := catalog.Refresh(t.Context()); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("refresh failures: %v", err)
			}
			query := Query{Terms: []string{"unknown"}, Since: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			result, err := catalog.Search(t.Context(), query)
			if !errors.Is(err, ErrIncomplete) || len(result.Issues) != 1 || result.OmittedIssues != 0 || filepath.Base(result.Issues[0].Path) != "recent.jsonl" || result.Issues[0].Record {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			compareIndexedSearch(t, catalog, query)
			query.Terms = []string{"absent"}
			compareIndexedSearch(t, catalog, query)
			query.Since = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
			result, err = catalog.Search(t.Context(), query)
			if err != nil || len(result.Issues) != 0 || result.OmittedIssues != 0 {
				t.Fatalf("unselected failures result=%#v err=%v", result, err)
			}
			if !unreadable {
				requireUnchangedRefresh(t, result, 2)
			}
			compareIndexedSearch(t, catalog, query)
		})
	}
}

func TestWarmTextSearchDoesNotReadUnchangedFilesWithIssues(t *testing.T) {
	t.Parallel()
	catalog := malformedIssueFixture(t)
	if _, err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"needle", "broken", "absent"} {
		t.Run(term, func(t *testing.T) {
			assertWarmTextSearchWithoutContent(t, catalog, term)
		})
	}
}

func assertWarmTextSearchWithoutContent(t *testing.T, catalog Catalog, term string) {
	t.Helper()
	index, err := openHistoryIndex(t.Context(), catalog.IndexPath, catalog.Sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	s := &search{index: index, mode: modeSearch, query: Query{Terms: []string{term}}}
	s.reset()
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	file, ok := s.singleFile(catalog.Sources[0], filepath.Join(catalog.Sources[0].Path, "recent.jsonl"))
	if !ok {
		t.Fatal("history was not selected")
	}
	opened := 0
	file.open = func() (*os.File, error) {
		opened++
		return nil, os.ErrPermission
	}
	var status SourceStatus
	index.scanFiles(t.Context(), s, catalog.Sources[0], []historyFile{file}, &status)
	want := 0
	if term == "needle" {
		want = 1
	}
	if opened != 0 || s.indexErr != nil || len(s.result.Matches) != want || len(s.result.Issues) != want || s.result.OmittedIssues != 0 {
		t.Fatalf("opens=%d result=%#v index error=%v", opened, s.result, s.indexErr)
	}
	requireUnchangedRefresh(t, s.result, 1)
}

func requireUnchangedRefresh(t *testing.T, result Result, count int) {
	t.Helper()
	if len(result.Refresh) != 1 {
		t.Fatalf("refresh sources=%#v", result.Refresh)
	}
	if diff := cmp.Diff(RefreshStats{Checked: count, Unchanged: count}, result.Refresh[0].RefreshStats); diff != "" {
		t.Fatalf("unchanged refresh accounting (-want +got):\n%s", diff)
	}
}

func TestRefreshReportsMalformedRecordsOnFreshAndWarmIndex(t *testing.T) {
	t.Parallel()
	catalog := filterFixture(t)
	appendRecord(t, filepath.Join(catalog.Sources[0].Path, "old.jsonl"), "{broken\n")
	for range 2 {
		result, err := catalog.Refresh(t.Context())
		if err != nil || len(result.Issues) != 1 || !result.Issues[0].Record || result.OmittedIssues != 0 {
			t.Fatalf("refresh=%#v err=%v", result, err)
		}
	}
}

func cappedIssueFixture(t *testing.T, unreadable bool) (Catalog, string) {
	t.Helper()
	catalog, root := childFixture(t)
	appendRecord(t, filepath.Join(root, "main.jsonl"), strings.Repeat("{broken\n", maxIssues))
	child := filepath.Join(root, "main/subagents/agent-3.jsonl")
	if unreadable {
		if err := os.Chmod(child, 0); err != nil {
			t.Fatal(err)
		}
	} else {
		appendRecord(t, child, "{broken\n")
	}
	return catalog, child
}

func TestCappedParentIssuesDoNotHideChildDiagnostics(t *testing.T) {
	t.Parallel()
	for _, unreadable := range []bool{false, true} {
		t.Run(strconv.FormatBool(unreadable), func(t *testing.T) {
			t.Parallel()
			catalog, child := cappedIssueFixture(t, unreadable)
			for _, tools := range []bool{false, true} {
				query := Query{Terms: []string{"gremlin"}, Dirs: []string{"/work/z"}, IncludeTools: tools}
				result, err := catalog.Search(t.Context(), query)
				if errors.Is(err, ErrIncomplete) != unreadable || len(result.Issues) != 1 || result.Issues[0].Path != child || result.OmittedIssues != 0 {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				compareIndexedSearch(t, catalog, query)
				if unreadable {
					query.Dirs = nil
					result, err = catalog.Search(t.Context(), query)
					if !errors.Is(err, ErrIncomplete) || len(result.Issues) != maxIssues || result.OmittedIssues != 1 {
						t.Fatalf("capped selected failures result=%#v err=%v", result, err)
					}
					compareIndexedSearch(t, catalog, query)
				}
			}
		})
	}
}

func TestChildHistoryIssuesFollowParentMetadata(t *testing.T) {
	t.Parallel()
	catalog, root := childFixture(t)
	child := filepath.Join(root, "main/subagents/agent-1.jsonl")
	file, err := os.OpenFile(child, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{broken\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		dir               string
		reported, omitted int
	}{{"/work/a", 1, 0}, {"/work/b", 0, 0}} {
		query := Query{Terms: []string{"gremlin"}, Dirs: []string{test.dir}}
		result, err := catalog.Search(t.Context(), query)
		if err != nil || len(result.Issues) != test.reported || result.OmittedIssues != test.omitted {
			t.Fatalf("dir=%s result=%#v err=%v", test.dir, result, err)
		}
		compareIndexedSearch(t, catalog, query)
	}
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
				q := Query{Terms: []string{"needle"}, IncludeTools: tools}
				got, err := c.searchDirect(t.Context(), q)
				wantParts := 1
				if tools {
					wantParts = tt.tools
				}
				if err != nil || len(got.Issues) != 1 || !got.Issues[0].Record || len(got.Matches) != 1 || got.Matches[0].MatchingParts != wantParts || got.Matches[0].Conversation.UpdatedAt.Format(time.RFC3339) != tt.updated {
					t.Fatalf("tools=%t: matches=%#v issues=%#v err=%v", tools, got.Matches, got.Issues, err)
				}
				compareIndexedSearch(t, c, q)
			}
		})
	}
}

func TestPrefilterPreservesPartsAndQueryModes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"sample","cwd":"/work"}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}
{"type":"message","message":{"role":"toolResult","content":"hidden-token"}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, test := range []struct {
		query Query
		want  int
	}{
		{Query{Terms: []string{"firstsecond"}}, 0},
		{Query{Terms: []string{"first", "second"}}, 1},
		{Query{Terms: []string{"first"}, Exclude: []string{"second"}}, 0},
		{Query{Terms: []string{"f.*t"}, Regex: true}, 1},
		{Query{Terms: []string{"first|second"}, Regex: true}, 1},
		{Query{Terms: []string{"first"}, Word: true}, 1},
		{Query{Terms: []string{"irs"}, Word: true}, 0},
		{Query{Terms: []string{"hidden-token"}, IncludeTools: true}, 1},
	} {
		got, err := catalog.Search(t.Context(), test.query)
		if err != nil || len(got.Matches) != test.want {
			t.Fatalf("query=%#v matches=%d want=%d err=%v", test.query, len(got.Matches), test.want, err)
		}
		compareIndexedSearch(t, catalog, test.query)
	}
	listed, err := catalog.listDirect(t.Context(), ListQuery{})
	if err != nil || len(listed.Matches) != 1 || listed.Matches[0].Conversation.Messages != 1 {
		t.Fatalf("metadata listing: %#v err=%v", listed, err)
	}
}

func TestInjectedContextSearchRoles(t *testing.T) {
	t.Parallel()
	body := `{"type":"session","id":"s","cwd":"/work"}
{"type":"message","id":"c","message":{"role":"user","content":"<environment_context>context-only</environment_context>"}}
{"type":"message","id":"u","message":{"role":"user","content":"<skill>context-prefix</skill>\n  typed needle\nsecond line"}}
{"type":"message","id":"x","message":{"role":"user","content":"<unfinished>literal needle"}}
{"type":"message","id":"s","message":{"role":"user","content":"<command-name>/review</command-name><command-args>needle</command-args>"}}
{"type":"message","id":"a","message":{"role":"assistant","content":"agent needle"}}
`
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, test := range []struct {
		name  string
		query Query
		parts int
	}{
		{"default hides context", Query{Text: "context"}, 0},
		{"tool opt in still hides context", Query{Text: "context", IncludeTools: true}, 0},
		{"context role", Query{Text: "context", Role: "context"}, 2},
		{"all includes context", Query{Text: "context", Role: "all"}, 2},
		{"user excludes prefix", Query{Text: "context-prefix", Role: "user"}, 0},
		{"typed excerpts", Query{Text: "needle", Excerpts: 10}, 4},
		{"context exclusions ignored", Query{Text: "needle", Exclude: []string{"context-prefix"}}, 4},
		{"context does not meet minimum", Query{Text: "needle", MinMessages: 5}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			match := requireSearchParts(t, catalog, test.query, test.parts, 4)
			if match == nil {
				return
			}
			if match.Conversation.Prompt != "typed needle second line" {
				t.Fatalf("prompt=%q", match.Conversation.Prompt)
			}
			if test.name != "typed excerpts" {
				return
			}
			texts := make([]string, 0, len(match.Excerpts))
			for _, excerpt := range match.Excerpts {
				texts = append(texts, excerpt.Text)
			}
			if diff := cmp.Diff([]string{"typed needle\nsecond line", "<unfinished>literal needle", "/review needle", "agent needle"}, texts); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestNativeContextSearchRoles(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		harness      registry.Harness
		name, body   string
		contextParts int
	}{
		{"claude", "session.jsonl", `{"type":"user","sessionId":"s","isMeta":true,"message":{"role":"user","content":"context-one"}}
{"type":"user","sessionId":"s","isCompactSummary":true,"message":{"role":"user","content":"context-two"}}
{"type":"user","sessionId":"s","isMeta":false,"message":{"role":"user","content":"typed"}}
`, 2},
		{"pi", "session.jsonl", `{"type":"session","id":"s"}
{"type":"custom_message","id":"c","content":[{"type":"text","text":"context-one"}]}
{"type":"message","id":"m","message":{"role":"custom","content":"context-two"}}
{"type":"message","id":"u","message":{"role":"user","content":"typed"}}
`, 2},
		{"omp", "session.jsonl", `{"type":"session","id":"s"}
{"type":"custom_message","id":"c","content":"context-one"}
{"type":"message","id":"m","message":{"role":"custom","content":"context-two"}}
{"type":"message","id":"u","message":{"role":"user","content":"typed"}}
`, 2},
		{"codex", "rollout-s.jsonl", `{"type":"session_meta","payload":{"id":"s"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":"<environment_context>context-one</environment_context>"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":"<literal>typed</literal>"}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u","content":[{"type":"text","text":"<literal>typed</literal>"}]}}}
{"type":"event_msg","payload":{"type":"user_message","message":"<literal>typed</literal>"}}
`, 1},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			catalog := Catalog{Sources: []Source{{Harness: test.harness, Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			for _, selection := range []struct {
				query Query
				parts int
			}{
				{Query{Text: "context"}, 0},
				{Query{Text: "context", Role: "context"}, test.contextParts},
				{Query{Text: "typed", Role: "user"}, 1},
			} {
				t.Run(selection.query.Text+selection.query.Role, func(t *testing.T) {
					requireSearchParts(t, catalog, selection.query, selection.parts, 1)
				})
			}
		})
	}
}

func TestNativeUserProjectionDoesNotDuplicateTypedText(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, response string
		contextParts   int
	}{
		{"plain", `"typed needle"`, 0},
		{"mixed", `"<skill>prefix-marker</skill>\ntyped needle"`, 1},
		{"empty", `""`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf(`{"type":"session_meta","payload":{"id":"s"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":%s}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_tokens":100}}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u","content":[{"type":"text","text":"typed needle"}]}}}
`, test.response)
			path := filepath.Join(t.TempDir(), "rollout-s.jsonl")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			catalog := Catalog{Sources: []Source{{Harness: registry.Harness("codex"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			requireSearchParts(t, catalog, Query{Text: "needle", Role: "context"}, 0, 1)
			match := requireSearchParts(t, catalog, Query{Text: "needle", Role: "all", Excerpts: 10}, 1, 1)
			if len(match.Excerpts) != 1 || match.Excerpts[0].Text != "typed needle" || match.Excerpts[0].Role != "user" {
				t.Fatalf("excerpts=%#v", match.Excerpts)
			}
			requireSearchParts(t, catalog, Query{Text: "prefix-marker", Role: "context"}, test.contextParts, 1)
		})
	}
}

func requireSearchParts(t *testing.T, catalog Catalog, query Query, parts, messages int) *Match {
	t.Helper()
	result, err := catalog.searchDirect(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	compareIndexedSearch(t, catalog, query)
	if parts == 0 {
		if len(result.Matches) != 0 {
			t.Fatalf("unexpected matches=%#v", result.Matches)
		}
		return nil
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches=%#v", result.Matches)
	}
	match := &result.Matches[0]
	if match.Conversation.Messages != messages || match.MatchingParts != parts {
		t.Fatalf("match=%#v", match)
	}
	return match
}

func TestNativeUserProjectionAcrossAppends(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollout-s.jsonl")
	body := `{"type":"session_meta","payload":{"id":"s"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":"first typed"}}
`
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("codex"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, stage := range []struct {
		addition string
		messages int
	}{
		{"", 1},
		{`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"first typed"}]}}}` + "\n", 1},
		{`{"type":"response_item","payload":{"type":"message","role":"user","content":"second typed"}}` + "\n", 1},
		{`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"second typed"}]}}}` + "\n", 2},
	} {
		body += stage.addition
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"", "user", "context", "all"} {
			compareIndexedSearch(t, catalog, Query{Text: "typed", Role: role})
		}
		requireSearchParts(t, catalog, Query{Text: "typed", Role: "context"}, 0, stage.messages)
		requireSearchParts(t, catalog, Query{Text: "typed", Role: "all", Excerpts: 10}, stage.messages, stage.messages)
		result, err := catalog.Search(t.Context(), Query{Text: "typed", Excerpts: 10})
		if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.Messages != stage.messages || result.Matches[0].MatchingParts != stage.messages {
			t.Fatalf("messages=%d result=%#v err=%v", stage.messages, result, err)
		}
	}
}

func TestIndexMatchesDirectScan(t *testing.T) {
	t.Parallel()
	c := parityFixture(t)
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(c.Sources[0].Path, linked); err != nil {
		t.Fatal(err)
	}
	sessions := []registry.Session{
		{ID: "first", Harness: c.Sources[0].Harness, SessionID: "s0", SessionPath: filepath.Join(linked, "0.jsonl")},
		{ID: "second", Harness: c.Sources[0].Harness, SessionPath: filepath.Join(linked, "1.jsonl")},
	}
	//nolint:gosmopolitan // Exercise literal matching of multi-byte Unicode queries.
	for _, text := range []string{"needle", "NEEDLE", "istanbul", "你好", "你好世", "%b_", `"quoted"`, "AND OR", "a", "é", "after-token", "\x00after", "not-present"} {
		for _, dirs := range [][]string{nil, {"/work/a"}, {"/"}, {"/work/a", "/work/b"}, {"/work", "/work/a"}} {
			for _, sensitive := range []bool{false, true} {
				for _, limit := range []int{0, 1, 2, 3} {
					q := Query{Terms: []string{text}, CaseSensitive: sensitive, Limit: limit, Dirs: dirs, Registry: sessions}
					compareIndexedSearch(t, c, q)
				}
			}
		}
	}
}

func TestIndexMatchesGeneratedMutationHistories(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(property *rapid.T) {
		root, rootDir := openGeneratedHistoryRoot(t, property)
		harness := rapid.SampledFrom([]registry.Harness{"pi", "omp", "codex", "claude"}).Draw(property, "harness")
		count := rapid.IntRange(2, 5).Draw(property, "transcripts")
		paths := make([]string, count)
		for index := range paths {
			parent := "bucket-" + strconv.Itoa(index)
			if err := root.MkdirAll(parent, 0o700); err != nil {
				property.Fatal(err)
			}
			paths[index] = filepath.Join(parent, "rollout-"+strconv.Itoa(index)+".jsonl")
			body := generatedHistory(property, harness, "native-"+strconv.Itoa(index))
			if err := root.WriteFile(paths[index], body, 0o600); err != nil {
				property.Fatal(err)
			}
		}
		catalog := Catalog{Sources: []Source{{Harness: harness, Path: rootDir}}, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}
		refreshGeneratedHistory(property, catalog)
		initial := compareGeneratedHistorySearch(property, catalog, Query{Terms: []string{"shared-token"}})
		if len(initial.Matches) != count {
			property.Fatalf("generated native transcripts produced %d matches, want %d", len(initial.Matches), count)
		}
		mutations := rapid.SliceOfN(rapid.SampledFrom([]string{"append", "rewrite", "replace", "truncate", "remove", "restore", "tail", "same-size", "move-tree", "remove-tree"}), 1, 20).Draw(property, "mutations")
		checkGeneratedHistoryQueries(property, catalog, count)
		for _, mutation := range mutations {
			index := rapid.IntRange(0, count-1).Draw(property, "transcript")
			paths[index] = mutateGeneratedHistoryPath(property, root, paths[index], harness, "native-"+strconv.Itoa(index), mutation)
			modified := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC).Add(time.Duration(rapid.IntRange(0, 86400*2).Draw(property, "modification time")) * time.Second)
			if err := root.Chtimes(paths[index], modified, modified); err != nil && !errors.Is(err, os.ErrNotExist) {
				property.Fatal(err)
			}
			if rapid.Bool().Draw(property, "refresh before search") {
				refreshGeneratedHistory(property, catalog)
			}
			checkGeneratedHistoryQueries(property, catalog, count)
		}
	})
}

func openGeneratedHistoryRoot(t *testing.T, property *rapid.T) (*os.Root, string) {
	t.Helper()
	property.Helper()
	rootDir := t.TempDir()
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		property.Fatal(err)
	}
	property.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root, rootDir
}

func refreshGeneratedHistory(property *rapid.T, catalog Catalog) {
	property.Helper()
	if _, err := catalog.Refresh(property.Context()); err != nil && !errors.Is(err, ErrIncomplete) {
		property.Fatal(err)
	}
}

func checkGeneratedHistoryQueries(property *rapid.T, catalog Catalog, count int) {
	for _, limit := range []int{0, 1, rapid.IntRange(2, count+2).Draw(property, "limit")} {
		compareGeneratedHistorySearch(property, catalog, Query{Terms: []string{"shared-token"}, Limit: limit})
	}
	query := generatedHistoryQuery(property, count)
	compareGeneratedHistorySearch(property, catalog, query)
	query.Registry = generatedRegistry(catalog)
	query.GitBranch = rapid.SampledFrom([]string{"", "main", "topic"}).Draw(property, "list branch")
	query.Since = rapid.SampledFrom([]time.Time{{}, time.Date(2026, 1, 2, 0, 0, 0, 1, time.UTC), time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)}).Draw(property, "list since")
	compareGeneratedHistoryList(property, catalog, ListQuery{Filter: query.Filter, Limit: query.Limit, Sort: SortUpdated})
}

func compareGeneratedHistorySearch(property *rapid.T, catalog Catalog, query Query) Result {
	property.Helper()
	query.Registry = generatedRegistry(catalog)
	want, wantErr := catalog.searchDirect(property.Context(), query)
	unfiltered := &search{mode: modeSearch, query: query}
	sources, err := catalog.begin(property.Context(), unfiltered)
	if err != nil {
		property.Fatal(err)
	}
	unfiltered.prefilter = nil
	_ = unfiltered.runDirect(property.Context(), sources)
	if diff := cmp.Diff(unfiltered.finalize(property.Context()).Matches, want.Matches); diff != "" {
		property.Fatalf("prefilter changed matches (-full +prefilter):\n%s", diff)
	}
	got, gotErr := catalog.Search(property.Context(), query)
	if (gotErr == nil) != (wantErr == nil) || errors.Is(gotErr, ErrIncomplete) != errors.Is(wantErr, ErrIncomplete) {
		property.Fatalf("query=%#v indexed error=%v direct error=%v", query, gotErr, wantErr)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
		property.Fatalf("query=%#v indexed result differs (-direct +indexed):\n%s", query, diff)
	}
	return got
}

func generatedRegistry(catalog Catalog) []registry.Session {
	return []registry.Session{
		{ID: "live", Harness: catalog.Sources[0].Harness, SessionID: "native-0", SessionPath: filepath.Join(catalog.Sources[0].Path, "rollout-0.jsonl"), Liveness: registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil)},
		{ID: "gone", Harness: catalog.Sources[0].Harness, SessionID: "native-1", Liveness: registry.NewLiveness(registry.PresenceGone, registry.ActivityValue(nil), nil)},
		{ID: "path-only", Harness: catalog.Sources[0].Harness, SessionPath: filepath.Join(catalog.Sources[0].Path, "rollout-0.jsonl")},
	}
}

func compareGeneratedHistoryList(property *rapid.T, catalog Catalog, query ListQuery) {
	property.Helper()
	want, wantErr := catalog.listDirect(property.Context(), query)
	got, gotErr := catalog.List(property.Context(), query)
	if (gotErr == nil) != (wantErr == nil) || errors.Is(gotErr, ErrIncomplete) != errors.Is(wantErr, ErrIncomplete) {
		property.Fatalf("list=%#v indexed error=%v direct error=%v", query, gotErr, wantErr)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
		property.Fatalf("list=%#v indexed result differs (-direct +indexed):\n%s", query, diff)
	}
}

func generatedHistoryQuery(property *rapid.T, count int) Query {
	terms := []string{"shared-token", "edited-token", "needle", "STRASSE", "\u0130stanbul", "\u754c", "absent-token"}
	query := Query{
		Terms:         rapid.SliceOfN(rapid.SampledFrom(terms), 1, 3).Draw(property, "terms"),
		Exclude:       rapid.SliceOfN(rapid.SampledFrom(terms), 0, 2).Draw(property, "exclude"),
		Regex:         rapid.Bool().Draw(property, "regex"),
		Word:          rapid.Bool().Draw(property, "word"),
		Role:          rapid.SampledFrom([]string{"", "user", "assistant", "context", "tool", "all"}).Draw(property, "role"),
		IncludeTools:  rapid.Bool().Draw(property, "include tools"),
		CaseSensitive: rapid.Bool().Draw(property, "case sensitive"),
		Excerpts:      rapid.IntRange(0, 5).Draw(property, "excerpts"),
		Sort:          rapid.SampledFrom([]Sort{SortUpdated, SortCreated, SortMatches, SortMessages}).Draw(property, "sort"),
		Limit:         rapid.IntRange(0, count+2).Draw(property, "query limit"),
		Dirs:          rapid.SliceOfN(rapid.SampledFrom([]string{"/work", "/work/alpha", "/work/beta", "/other"}), 0, 4).Draw(property, "directories"),
		ExcludeDirs:   rapid.SliceOfN(rapid.SampledFrom([]string{"/work/alpha", "/work/beta"}), 0, 1).Draw(property, "excluded directories"),
		Model:         rapid.SampledFrom([]string{"", "model", "large", "absent"}).Draw(property, "model"),
		MinMessages:   rapid.IntRange(0, 8).Draw(property, "minimum messages"),
		Presence:      rapid.SampledFrom([]registry.Presence{"", registry.PresenceLive, registry.PresenceGone}).Draw(property, "presence"),
		Since:         rapid.SampledFrom([]time.Time{{}, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)}).Draw(property, "since"),
	}
	if query.Regex {
		for index, term := range query.Terms {
			query.Terms[index] = regexp.QuoteMeta(term)
		}
		for index, term := range query.Exclude {
			query.Exclude[index] = regexp.QuoteMeta(term)
		}
	}
	return query
}

func generatedHistory(property *rapid.T, harness registry.Harness, id string) []byte {
	property.Helper()
	cwd := rapid.SampledFrom([]string{"/work/alpha", "/work/beta/subdir", "/other"}).Draw(property, "cwd")
	timestamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rapid.IntRange(0, 86400).Draw(property, "session time")) * time.Second).Format(time.RFC3339)
	var header map[string]any
	switch harness {
	case "pi", "omp":
		header = map[string]any{"type": "session", "id": id, "cwd": cwd, "timestamp": timestamp}
	case "codex":
		header = map[string]any{"type": "session_meta", "timestamp": timestamp, "payload": map[string]any{"id": id, "cwd": cwd, "model": "large-model"}}
	case "claude":
		header = map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": timestamp, "message": map[string]any{"role": "user", "content": "shared-token"}}
	}
	body := marshalGeneratedHistoryRecord(property, header)
	nativeUsers := rapid.Bool().Draw(property, "native users")
	body = append(body, generatedHistoryMessage(property, harness, id, "user", "shared-token "+rapid.String().Draw(property, "prompt"), nativeUsers)...)
	for range rapid.IntRange(1, 6).Draw(property, "message count") {
		role := rapid.SampledFrom([]string{"user", "assistant", "toolResult"}).Draw(property, "message role")
		text := rapid.SampledFrom([]string{"needle", "Stra\u00dfe", "\u0130stanbul", "\u754c", "shared-token"}).Draw(property, "message term") + " " + rapid.String().Draw(property, "message text")
		if role == "user" {
			switch rapid.SampledFrom([]string{"plain", "injected", "mixed"}).Draw(property, "user content") {
			case "injected":
				text = "<skill>" + text + "</skill>"
			case "mixed":
				text = "<environment_context>shared-token</environment_context>\n" + text
			}
		}
		body = append(body, generatedHistoryMessage(property, harness, id, role, text, nativeUsers)...)
	}
	return body
}

func generatedHistoryMessage(property *rapid.T, harness registry.Harness, id, role, text string, nativeUsers bool) []byte {
	timestamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Add(time.Duration(rapid.IntRange(0, 86400).Draw(property, "message time")) * time.Second).Format(time.RFC3339)
	message := map[string]any{"role": role, "content": text, "model": "large-model"}
	var record map[string]any
	switch harness {
	case "pi", "omp":
		record = map[string]any{"type": "message", "timestamp": timestamp, "message": message}
	case "codex":
		payload := map[string]any{"type": "message", "role": role, "content": []map[string]any{{"type": "input_text", "text": text}}}
		if role == "toolResult" {
			payload = map[string]any{"type": "function_call_output", "output": text}
		}
		record = map[string]any{"type": "response_item", "timestamp": timestamp, "payload": payload}
	case "claude":
		record = map[string]any{"type": role, "sessionId": id, "timestamp": timestamp, "message": message}
	}
	data := marshalGeneratedHistoryRecord(property, record)
	if nativeUsers && harness == "codex" && role == "user" {
		event := map[string]any{"type": "event_msg", "timestamp": timestamp, "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": id, "content": []map[string]any{{"type": "text", "text": text}}}}}
		data = append(data, marshalGeneratedHistoryRecord(property, event)...)
	}
	return data
}

func marshalGeneratedHistoryRecord(property *rapid.T, record map[string]any) []byte {
	property.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		property.Fatal(err)
	}
	return append(data, '\n')
}

func mutateGeneratedHistoryPath(property *rapid.T, root *os.Root, path string, harness registry.Harness, id, mutation string) string {
	property.Helper()
	parent := filepath.Dir(path)
	switch mutation {
	case "move-tree":
		destination := parent + "-moved"
		if err := root.Rename(parent, destination); err == nil {
			return filepath.Join(destination, filepath.Base(path))
		} else if !errors.Is(err, os.ErrNotExist) {
			property.Fatal(err)
		}
	case "remove-tree":
		if err := root.RemoveAll(parent); err != nil {
			property.Fatal(err)
		}
	default:
		if err := root.MkdirAll(parent, 0o700); err != nil {
			property.Fatal(err)
		}
		mutateGeneratedHistory(property, root, path, harness, id, mutation)
	}
	return path
}

func mutateGeneratedHistory(property *rapid.T, root *os.Root, path string, harness registry.Harness, id, mutation string) {
	property.Helper()
	body, err := root.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		property.Fatal(err)
	}
	if mutation == "remove" {
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			property.Fatal(err)
		}
		return
	}
	if mutation == "same-size" {
		mutateHistorySameSize(property, root, path, body)
		return
	}
	body = generatedHistoryMutation(property, harness, id, mutation, body)
	if mutation == "replace" {
		pathReplacement := path + ".replacement"
		if err := root.WriteFile(pathReplacement, body, 0o600); err != nil {
			property.Fatal(err)
		}
		if err := root.Rename(pathReplacement, path); err != nil {
			property.Fatal(err)
		}
		return
	}
	if err := root.WriteFile(path, body, 0o600); err != nil {
		property.Fatal(err)
	}
}

func generatedHistoryMutation(property *rapid.T, harness registry.Harness, id, mutation string, body []byte) []byte {
	switch mutation {
	case "append", "tail":
		record := generatedHistoryMessage(property, harness, id, "user", "shared-token "+rapid.String().Draw(property, "appended text"), rapid.Bool().Draw(property, "native appended user"))
		if mutation == "tail" {
			record = record[:rapid.IntRange(0, len(record)-1).Draw(property, "partial record bytes")]
		}
		return append(body, record...)
	case "rewrite", "restore":
		return generatedHistory(property, harness, id)
	case "replace":
		return generatedHistory(property, harness, "replacement-"+id)
	case "truncate":
		return body[:rapid.IntRange(0, len(body)).Draw(property, "remaining bytes")]
	default:
		property.Fatalf("unknown mutation %q", mutation)
		return nil
	}
}

func mutateHistorySameSize(property *rapid.T, root *os.Root, path string, body []byte) {
	property.Helper()
	info, err := root.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		property.Fatal(err)
	}
	changed := strings.ReplaceAll(string(body), "shared-token", "edited-token")
	if err := root.WriteFile(path, []byte(changed), 0o600); err != nil {
		property.Fatal(err)
	}
	if err := root.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		property.Fatal(err)
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

func compareIndexedSearch(t *testing.T, c Catalog, q Query) {
	t.Helper()
	want, wantErr := c.searchDirect(t.Context(), q)
	got, err := c.Search(t.Context(), q)
	if errors.Is(err, ErrIncomplete) != errors.Is(wantErr, ErrIncomplete) {
		t.Fatalf("query %#v errors: %v / %v", q, err, wantErr)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
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
		{"all", Query{Terms: []string{"needle"}}, 1000},
		{"limited", Query{Terms: []string{"needle"}, Limit: 10}, 10},
		{"directory", Query{Terms: []string{"needle"}, Dirs: []string{"/work/selected"}}, 10},
		{"missing", Query{Terms: []string{"absent-token"}, Dirs: []string{"/work/selected"}}, 0},
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
	if _, err := c.Search(b.Context(), Query{Terms: []string{"needle"}}); err != nil {
		b.Fatal(err)
	}
	return c
}

func registryBenchmarkCatalog(b *testing.B) (Catalog, []registry.Session) {
	b.Helper()
	root := b.TempDir()
	linked := filepath.Join(b.TempDir(), "linked")
	if err := os.Symlink(root, linked); err != nil {
		b.Fatal(err)
	}
	sources := make([]Source, 0, 2)
	var sessions []registry.Session
	for _, harness := range []registry.Harness{"pi", "omp"} {
		dir := filepath.Join(root, string(harness))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		sources = append(sources, Source{Harness: harness, Path: dir})
		for number := range 5000 {
			name := fmt.Sprintf("%04d.jsonl", number)
			id := fmt.Sprintf("session-%d", number)
			body := fmt.Sprintf("{\"type\":\"session\",\"id\":%q,\"cwd\":\"/work/project\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"example needle\"}}\n", id)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				b.Fatal(err)
			}
			if number < 15 {
				sessions = append(sessions, registry.Session{ID: fmt.Sprintf("%s-%d", harness, number), Harness: harness, SessionID: id, SessionPath: filepath.Join(linked, string(harness), name)})
			}
		}
	}
	catalog := Catalog{Sources: sources, IndexPath: filepath.Join(b.TempDir(), "index.sqlite")}
	if _, err := catalog.Refresh(b.Context()); err != nil {
		b.Fatal(err)
	}
	return catalog, sessions
}

func BenchmarkHistoryRegistrySearch(b *testing.B) {
	catalog, sessions := registryBenchmarkCatalog(b)
	for _, mode := range []string{"list", "text", "recent"} {
		for _, withRegistry := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/registry=%t", mode, withRegistry), func(b *testing.B) {
				filter := Filter{MinMessages: 0}
				if withRegistry {
					filter.Registry = sessions
				}
				benchmarkRegistryQuery(b, catalog, filter, mode)
			})
		}
	}
}

func benchmarkRegistryQuery(b *testing.B, catalog Catalog, filter Filter, mode string) {
	b.Helper()
	want := 20
	if mode == "recent" {
		filter.Since = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		want = 0
	}
	b.ReportAllocs()
	for b.Loop() {
		var result Result
		var err error
		if mode != "text" {
			result, err = catalog.List(b.Context(), ListQuery{Filter: filter, Limit: 20})
		} else {
			result, err = catalog.Search(b.Context(), Query{Filter: filter, Terms: []string{"needle"}, Limit: 20})
		}
		if err != nil || len(result.Matches) != want {
			b.Fatalf("matches=%d, %v", len(result.Matches), err)
		}
	}
}

func TestSearchLeavesUnneededFilesUnindexed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		modified int
		query    Query
		stored   int
	}{
		{"old file", 2, Query{Terms: []string{"token"}, Since: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}, 0},
		{"recent file with old activity", 4, Query{Terms: []string{"token"}, Since: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}, 1},
		{"absent text", 4, Query{Terms: []string{"absent"}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "session.jsonl")
			body := claudeRecord("user", "sample", "/work", "main", "2026-01-01T00:00:00Z", "token", "")
			modified := time.Date(2026, 1, test.modified, 0, 0, 0, 0, time.UTC)
			writeHistoryAt(t, path, []byte(body), modified)
			catalog := Catalog{Sources: []Source{{Harness: registry.Harness("claude"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			compareIndexedSearch(t, catalog, test.query)
			result, err := catalog.Search(t.Context(), test.query)
			if err != nil || len(result.Matches) != 0 {
				t.Fatalf("filtered result: %#v err=%v", result, err)
			}
			requireIndexedFiles(t, catalog.IndexPath, test.stored)
			if !test.query.Since.IsZero() {
				listed, err := catalog.List(t.Context(), ListQuery{Filter: test.query.Filter})
				if err != nil || len(listed.Matches) != 0 {
					t.Fatalf("bounded listing: %#v err=%v", listed, err)
				}
				requireIndexedFiles(t, catalog.IndexPath, test.stored)
			}
			listed, err := catalog.List(t.Context(), ListQuery{})
			if err != nil || len(listed.Matches) != 1 {
				t.Fatalf("later listing: %#v err=%v", listed, err)
			}
			requireIndexedFiles(t, catalog.IndexPath, 1)
		})
	}
}

func writeHistoryAt(t *testing.T, path string, body []byte, modified time.Time) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func requireIndexedFiles(t *testing.T, path string, want int) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	var count int
	if err := database.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&count); err != nil || count != want {
		t.Fatalf("stored files=%d want=%d err=%v", count, want, err)
	}
}

func TestSkippedRewriteDoesNotReturnStaleMatches(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	query := Query{Terms: []string{"needle"}, CaseSensitive: true}
	compareIndexedSearch(t, catalog, query)
	path := filepath.Join(catalog.Sources[0].Path, "0.jsonl")
	body := `{"type":"session","id":"s0","cwd":"/work"}` + "\n" + `{"type":"message","message":{"role":"user","content":"replacement"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	compareIndexedSearch(t, catalog, query)
	result, err := catalog.Search(t.Context(), query)
	if err != nil || !slices.Equal(sessionIDs(result), []string{"s1"}) {
		t.Fatalf("stale rewrite: %#v err=%v", result, err)
	}
	compareIndexedSearch(t, catalog, Query{Terms: []string{"replacement"}})
}

func TestFallbackSkipsOldHistories(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"corrupt", "foreign", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			old := filepath.Join(root, "old.jsonl")
			modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			writeHistoryAt(t, old, []byte("invalid history"), modified)
			indexPath := filepath.Join(t.TempDir(), "index.sqlite")
			switch kind {
			case "foreign":
				createForeignTestDB(t, t.Context(), indexPath)
			case "corrupt":
				if err := os.WriteFile(indexPath, []byte("not a database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unavailable":
				if err := os.Mkdir(indexPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: indexPath}
			query := Query{Terms: []string{"anything"}, Since: modified.Add(time.Hour)}
			compareIndexedSearch(t, catalog, query)
			listed, err := catalog.List(t.Context(), ListQuery{Since: query.Since})
			if err != nil || len(listed.Matches) != 0 || len(listed.Issues) != 0 {
				t.Fatalf("fallback: %#v err=%v", listed, err)
			}
		})
	}
}

func TestDatabaseModificationTimeIncludesJournals(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"-wal", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.sqlite")
			old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, name := range []string{path, path + suffix} {
				writeHistoryAt(t, name, nil, old)
			}
			file := historyFile{path: path, database: true, stat: func() (os.FileInfo, error) { return os.Stat(path) }}
			filter := Filter{Since: old.Add(time.Hour)}
			if file.mayMatchTime(filter) {
				t.Fatal("old database passed the time filter")
			}
			if err := os.Chtimes(path+suffix, filter.Since, filter.Since); err != nil {
				t.Fatal(err)
			}
			if !file.mayMatchTime(filter) {
				t.Fatal("database with a recent journal was skipped")
			}
		})
	}
}

func TestRefreshRetriesFailedBatchFilesIndividually(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := Source{Harness: registry.Harness("pi"), Path: root}
	catalog := Catalog{Sources: []Source{source}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, name := range []string{"first", "bad", "last"} {
		body := fmt.Sprintf("{\"type\":\"session\",\"id\":%q}\n{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":%q}}\n", name, name)
		if err := os.WriteFile(filepath.Join(root, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	index := openTestHistoryIndex(t, catalog)
	if _, err := index.conn.ExecContext(t.Context(), "CREATE TRIGGER reject_part BEFORE INSERT ON parts WHEN new.body='bad' BEGIN SELECT RAISE(FAIL,'rejected history'); END"); err != nil {
		t.Fatal(err)
	}
	var progress Progress
	catalog.Progress = func(current Progress) { progress = current }
	result, err := catalog.Refresh(t.Context())
	if !errors.Is(err, ErrIncomplete) || len(result.Issues) != 1 || result.Issues[0].Path != filepath.Join(root, "bad.jsonl") || progress.Refreshed != 2 || progress.Done != 3 {
		t.Fatalf("refresh: progress=%#v result=%#v err=%v", progress, result, err)
	}
	var bodies string
	var files int
	if err := index.conn.QueryRowContext(t.Context(), "SELECT group_concat(body,','),(SELECT count(*) FROM files) FROM (SELECT body FROM parts ORDER BY body)").Scan(&bodies, &files); err != nil || bodies != "first,last" || files != 2 {
		t.Fatalf("stored text=%q files=%d err=%v", bodies, files, err)
	}
}

func TestRefreshCancellationDiscardsPendingBatch(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	if _, err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := filepath.Join(catalog.Sources[0].Path, "0.jsonl")
	appendTranscriptLine(t, path, "pending", "pending token")
	catalog.Progress = func(progress Progress) {
		if progress.Done == 1 {
			cancel()
		}
	}
	if _, err := catalog.Refresh(ctx); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("refresh error=%v, want cancellation", err)
	}
	index, err := openHistoryIndex(t.Context(), catalog.IndexPath, catalog.Sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := index.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE message_id='pending'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncommitted parts=%d err=%v", count, err)
	}
}

func TestBatchCancellationPreservesEarlierCommit(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	writeIndexFixture(t, catalog, "2.jsonl", "{\"type\":\"session\",\"id\":\"third\"}\n{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"third token\"}}\n")
	index := openTestHistoryIndex(t, catalog)
	entries := fixtureRefreshEntries(t, catalog, "0.jsonl", "1.jsonl", "2.jsonl")
	if _, err := index.storeBatch(t.Context(), entries[:1]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	insertions := 0
	setIndexHooks(t, index, func(hooks sqlite.HookRegisterer) {
		hooks.RegisterPreUpdateHook(func(update sqlite.SQLitePreUpdateData) {
			if update.TableName == "files" && update.Op == sqlite3.SQLITE_INSERT {
				insertions++
				if insertions == 2 {
					cancel()
				}
			}
		})
	})
	if _, err := index.storeBatch(ctx, entries[1:]); !errors.Is(err, context.Canceled) {
		t.Fatalf("store batch error=%v, want cancellation", err)
	}
	var files, conversations int
	if err := index.conn.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM files),(SELECT count(*) FROM conversations)").Scan(&files, &conversations); err != nil || files != 1 || conversations != 1 {
		t.Fatalf("after cancellation: files=%d conversations=%d err=%v", files, conversations, err)
	}
}

func TestSearchDuringBatchCommitMatchesDirect(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	index, err := openHistoryIndex(t.Context(), catalog.IndexPath, catalog.Sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	entries := fixtureRefreshEntries(t, catalog, "0.jsonl", "1.jsonl")
	committing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	setIndexHooks(t, index, func(hooks sqlite.HookRegisterer) {
		hooks.RegisterCommitHook(func() int32 {
			once.Do(func() {
				close(committing)
				<-release
			})
			return 0
		})
	})
	stored := make(chan error, 1)
	go func() {
		_, err := index.storeBatch(t.Context(), entries)
		stored <- err
	}()
	<-committing
	searched := make(chan struct{})
	var got Result
	var searchErr error
	go func() {
		got, searchErr = catalog.Search(t.Context(), Query{Terms: []string{"needle"}})
		close(searched)
	}()
	close(release)
	if err := <-stored; err != nil {
		t.Fatal(err)
	}
	<-searched
	want, err := catalog.searchDirect(t.Context(), Query{Terms: []string{"needle"}})
	if err != nil || searchErr != nil {
		t.Fatalf("direct error=%v search error=%v", err, searchErr)
	}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
		t.Fatalf("concurrent search differs (-direct +indexed):\n%s", diff)
	}
}

func openTestHistoryIndex(t *testing.T, catalog Catalog) *historyIndex {
	t.Helper()
	index, err := openHistoryIndex(t.Context(), catalog.IndexPath, catalog.Sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := index.close(t.Context()); err != nil {
			t.Error(err)
		}
	})
	return index
}

func fixtureRefreshEntries(t *testing.T, catalog Catalog, names ...string) []refreshEntry {
	t.Helper()
	entries := make([]refreshEntry, 0, len(names))
	for _, name := range names {
		file, ok := new(search).singleFile(catalog.Sources[0], filepath.Join(catalog.Sources[0].Path, name))
		if !ok {
			t.Fatal("history file was not selected")
		}
		job := &refreshJob{source: catalog.Sources[0], file: file}
		result := parseRefresh(t.Context(), job)
		if err := errors.Join(result.err, result.failure, result.openErr); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, refreshEntry{job: job, result: result})
	}
	return entries
}

func setIndexHooks(t *testing.T, index *historyIndex, register func(sqlite.HookRegisterer)) {
	t.Helper()
	if err := index.conn.Raw(func(connection any) error {
		hooks, ok := connection.(sqlite.HookRegisterer)
		if !ok {
			t.Fatalf("SQLite connection %T does not support hooks", connection)
		}
		register(hooks)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestIndexCanceledRefreshRollsBack(t *testing.T) {
	t.Parallel()
	c := parityFixture(t)
	if _, err := c.Search(t.Context(), Query{Terms: []string{"needle"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	index, err := openHistoryIndex(ctx, c.IndexPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(ctx); err != nil {
			t.Error(err)
		}
	}()
	source := c.Sources[0]
	path := filepath.Join(source.Path, "0.jsonl")
	writer := new(indexWriter)
	writer.append(Excerpt{Role: "user", Text: "canceled token"})
	writer.finish(Conversation{Harness: source.Harness, Path: path, SessionID: "uncommitted"})
	job := &refreshJob{source: source, file: historyFile{path: path}, previous: index.files[fileKey(source, path)], exists: true}
	if err := index.beginWrite(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := index.writeRefresh(ctx, job, refreshResult{stamp: "changed", writer: writer}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := index.endWrite(ctx); !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("commit after cancel = %v", err)
	}
	if err := index.close(ctx); err != nil {
		t.Fatal(err)
	}
	requireNoUncommittedConversations(t, c.IndexPath)
	compareIndexedSearch(t, c, Query{Terms: []string{"needle"}})
	compareIndexedSearch(t, c, Query{Terms: []string{"canceled token"}})
}

func requireNoUncommittedConversations(t *testing.T, indexPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", indexPath)
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
	if _, err := catalog.Search(ctx, Query{Terms: []string{"seed token"}}); err != nil {
		t.Fatal(err)
	}

	first, second := openTwoTestIndexes(t, ctx, indexPath, sources)
	defer func() { _ = first.close(ctx) }()
	defer func() { _ = second.close(ctx) }()

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
	first, err := openHistoryIndex(ctx, indexPath, sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openHistoryIndex(ctx, indexPath, sources, nil)
	if err != nil {
		_ = first.close(ctx)
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
	s := &search{index: index, mode: modeSearch, query: Query{Terms: []string{needle}, Limit: 100}}
	s.reset()
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	file, ok := s.singleFile(source, path)
	if !ok {
		t.Fatalf("history file %s was not selected", path)
	}
	var status SourceStatus
	index.scanFiles(ctx, s, source, []historyFile{file}, &status)
	if s.indexErr != nil || len(s.result.Issues) != 0 {
		t.Fatalf("index %s: %v %#v", path, s.indexErr, s.result.Issues)
	}
}

func TestForeignDatabaseNotAltered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	foreignPath := filepath.Join(t.TempDir(), "foreign.sqlite")
	beforeHash := createForeignTestDB(t, ctx, foreignPath)

	sources := []Source{{Harness: registry.Harness("pi"), Path: t.TempDir()}}
	idx, err := openHistoryIndex(ctx, foreignPath, sources, nil)
	if idx != nil {
		defer func() { _ = idx.close(ctx) }()
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

func claudeRecord(kind, session, cwd, branch, at, text, model string) string {
	message := map[string]any{"role": kind, "content": text}
	if model != "" {
		message["model"] = model
	}
	data, err := json.Marshal(map[string]any{"type": kind, "sessionId": session, "cwd": cwd, "gitBranch": branch, "timestamp": at, "uuid": session + at, "message": message})
	if err != nil {
		panic(err)
	}
	return string(data) + "\n"
}

// filterFixture holds three Claude conversations that differ in every
// filterable field, so each query below selects a distinct subset.
func filterFixture(t *testing.T) Catalog {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"old.jsonl": claudeRecord("user", "old", "/work/a", "main", "2026-01-01T10:00:00Z", "deploy the service", "") +
			claudeRecord("assistant", "old", "/work/a", "main", "2026-01-01T10:01:00Z", "deployment finished with token abc", "model-opus-1"),
		"mid.jsonl": claudeRecord("user", "mid", "/work/a/sub", "main", "2026-05-01T10:00:00Z", "dates like 2026-05-01 appear here", "") +
			claudeRecord("assistant", "mid", "/work/a/sub", "main", "2026-05-01T10:01:00Z", "ok", ""),
		"new.jsonl": claudeRecord("user", "new", "/work/b", "feature", "2026-09-01T10:00:00Z", "fix the token refresh", "") +
			claudeRecord("assistant", "new", "/work/b", "feature", "2026-09-01T10:01:00Z", "refreshed; tokens rotate now", "model-sonnet-2") +
			claudeRecord("user", "new", "/work/b", "feature", "2026-09-01T10:02:00Z", "thanks", ""),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Catalog{Sources: []Source{{Harness: registry.Harness("claude"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
}

func sessionIDs(result Result) []string {
	ids := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		ids = append(ids, match.Conversation.SessionID)
	}
	return ids
}

func TestQueriesAndFiltersMatchDirectScan(t *testing.T) {
	t.Parallel()
	c := filterFixture(t)
	day := func(value string) time.Time {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	tests := []struct {
		name  string
		query Query
		want  []string
	}{
		{"every term must match", Query{Terms: []string{"token", "refresh"}}, []string{"new"}},
		{"terms may match different messages", Query{Terms: []string{"thanks", "rotate"}}, []string{"new"}},
		{"exclusion drops conversations", Query{Terms: []string{"token"}, Exclude: []string{"deploy"}}, []string{"new"}},
		{"word boundaries", Query{Terms: []string{"tokens"}, Word: true}, []string{"new"}},
		{"word rejects longer words", Query{Terms: []string{"refresh"}, Word: true}, []string{"new"}},
		{"word rejects prefixes", Query{Terms: []string{"deploy"}, Word: true}, []string{"old"}},
		{"regex without a usable literal", Query{Terms: []string{`\d{4}-\d{2}-\d{2}`}, Regex: true}, []string{"mid"}},
		{"regex with a literal", Query{Terms: []string{`refresh(ed)?\b`}, Regex: true}, []string{"new"}},
		{"regex is case-insensitive by default", Query{Terms: []string{`DEPLOY\w+`}, Regex: true}, []string{"old"}},
		{"since keeps recent activity", Query{Terms: []string{"token"}, Since: day("2026-06-01")}, []string{"new"}},
		{"until keeps earlier starts", Query{Terms: []string{"token"}, Until: day("2026-02-01")}, []string{"old"}},
		{"branch", Query{Terms: []string{"o"}, GitBranch: "main"}, []string{"mid", "old"}},
		{"model substring", Query{Terms: []string{"token"}, Model: "OPUS"}, []string{"old"}},
		{"minimum messages", Query{Terms: []string{"t"}, MinMessages: 3}, []string{"new"}},
		{"excluded directory subtree", Query{Terms: []string{"o"}, ExcludeDirs: []string{"/work/a"}}, []string{"new"}},
		{"directory subtree", Query{Terms: []string{"o"}, Dirs: []string{"/work/a"}}, []string{"mid", "old"}},
		{"directory union", Query{Terms: []string{"o"}, Dirs: []string{"/work/a", "/work/b"}}, []string{"new", "mid", "old"}},
		{"nested directories do not duplicate", Query{Terms: []string{"o"}, Dirs: []string{"/work/a", "/work/a/sub"}}, []string{"mid", "old"}},
		{"directory exclusions still apply", Query{Terms: []string{"o"}, Dirs: []string{"/work", "/work/a"}, ExcludeDirs: []string{"/work/a"}}, []string{"new"}},
		{"other harness", Query{Terms: []string{"token"}, Harnesses: []registry.Harness{"codex"}}, []string{}},
		{"sort by matching parts", Query{Terms: []string{"token"}, Sort: SortMatches}, []string{"new", "old"}},
		{"sort by creation keeps limit order", Query{Terms: []string{"e"}, Sort: SortCreated, Limit: 2}, []string{"new", "mid"}},
		{"sort by messages", Query{Terms: []string{"e"}, Sort: SortMessages, Limit: 1}, []string{"new"}},
		{"case-sensitive literal rejects other case", Query{Terms: []string{"TOKEN"}, CaseSensitive: true}, []string{}},
		{"case-sensitive literal", Query{Terms: []string{"token"}, CaseSensitive: true}, []string{"new", "old"}},
		{"case-sensitive regex rejects other case", Query{Terms: []string{`Deploy\w*`}, Regex: true, CaseSensitive: true}, []string{}},
		{"inline case folding in a case-sensitive regex", Query{Terms: []string{`(?i)DEPLOY`}, Regex: true, CaseSensitive: true}, []string{"old"}},
		{"role", Query{Terms: []string{"token"}, Role: "user"}, []string{"new"}},
	}
	for _, tt := range tests {
		got, err := c.Search(t.Context(), tt.query)
		if errors.Is(err, ErrIncomplete) && len(tt.query.Harnesses) > 0 {
			err = nil
		}
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if diff := cmp.Diff(tt.want, sessionIDs(got)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
		compareIndexedSearch(t, c, tt.query)
	}
}

func TestRelativeDirectorySpellingsDoNotDuplicateConversations(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	body := fmt.Sprintf(`{"type":"session","id":"session","cwd":%q}`+"\n"+`{"type":"message","message":{"role":"user","content":"needle"}}`+"\n", root)
	if err := os.WriteFile("session.jsonl", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	query := Query{Terms: []string{"needle"}, Dirs: []string{".", root, "./"}}
	result, err := catalog.Search(t.Context(), query)
	if err != nil || !slices.Equal(sessionIDs(result), []string{"session"}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	compareIndexedSearch(t, catalog, query)
}

func TestListMatchesDirectScan(t *testing.T) {
	t.Parallel()
	c := filterFixture(t)
	for _, tt := range []struct {
		query ListQuery
		want  []string
	}{
		{ListQuery{}, []string{"new", "mid", "old"}},
		{ListQuery{Dirs: []string{"/work/a"}}, []string{"mid", "old"}},
		{ListQuery{Sort: SortMessages, Limit: 1}, []string{"new"}},
		{ListQuery{MinMessages: 3}, []string{"new"}},
	} {
		want, wantErr := c.listDirect(t.Context(), tt.query)
		got, err := c.List(t.Context(), tt.query)
		if err != nil || wantErr != nil {
			t.Fatalf("list %#v: %v / %v", tt.query, err, wantErr)
		}
		if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
			t.Fatalf("list %#v (-direct +index):\n%s", tt.query, diff)
		}
		if diff := cmp.Diff(tt.want, sessionIDs(got)); diff != "" {
			t.Fatalf("list %#v (-want +got):\n%s", tt.query, diff)
		}
		for _, match := range got.Matches {
			if len(match.Excerpts) != 0 || match.MatchingParts != 0 {
				t.Fatalf("list returned search data: %#v", match)
			}
		}
	}
}

func TestListPrefilterKeepsParentOwnership(t *testing.T) {
	t.Parallel()
	catalog, _ := childFixture(t)
	for _, scenario := range []struct {
		query     ListQuery
		want      []string
		truncated bool
	}{
		{ListQuery{Dirs: []string{"/work/a"}}, []string{"main"}, false},
		{ListQuery{Dirs: []string{"/work/z"}}, []string{"lost", "stray"}, false},
		{ListQuery{GitBranch: "topic"}, []string{"lost", "stray"}, false},
		{ListQuery{GitBranch: "topic' OR 1=1 --"}, nil, false},
		{ListQuery{MinMessages: 2}, []string{"main"}, false},
		{ListQuery{MinMessages: 0, Limit: 1}, []string{"lost"}, true},
	} {
		got, err := catalog.List(t.Context(), scenario.query)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(scenario.want, slices.Sorted(slices.Values(sessionIDs(got)))); diff != "" {
			t.Fatalf("query=%#v (-want +got):\n%s", scenario.query, diff)
		}
		if got.Truncated != scenario.truncated {
			t.Fatalf("query=%#v truncated=%t", scenario.query, got.Truncated)
		}
		want, err := catalog.listDirect(t.Context(), scenario.query)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
			t.Fatalf("query=%#v (-direct +indexed):\n%s", scenario.query, diff)
		}
	}
}

func TestListReadsRefreshedMetadataAfterPrefetch(t *testing.T) {
	t.Parallel()
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	firstPath, secondPath := filepath.Join(firstRoot, "first.jsonl"), filepath.Join(secondRoot, "second.jsonl")
	for _, path := range []string{firstPath, secondPath} {
		body := "{\"type\":\"session\",\"id\":\"session\",\"cwd\":\"/work/project\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"type\":\"message\",\"message\":{\"role\":\"user\",\"content\":\"earlier text\"}}\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: firstRoot}, {Harness: registry.Harness("pi"), Path: secondRoot}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	if _, err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalog.Progress = func(progress Progress) {
		if progress.Source.Path == firstRoot {
			appendRecord(t, secondPath, "{\"type\":\"message\",\"timestamp\":\"2026-01-03T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"new activity\"}}\n")
		}
	}
	query := ListQuery{Since: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), MinMessages: 2}
	got, err := catalog.List(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Matches) != 1 || got.Matches[0].Conversation.Path != secondPath || got.Matches[0].Conversation.Messages != 2 {
		t.Fatalf("refreshed list=%#v", got.Matches)
	}
}

func TestListFoldsNewChildIntoRejectedParent(t *testing.T) {
	t.Parallel()
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(firstRoot, "first.jsonl"), []byte(claudeRecord("user", "first", "/work/a", "main", "2026-01-01T00:00:00Z", "first prompt", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondRoot, "main.jsonl"), []byte(claudeRecord("user", "main", "/work/a", "main", "2026-01-01T00:00:00Z", "parent prompt", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("claude"), Path: firstRoot}, {Harness: registry.Harness("claude"), Path: secondRoot}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	if _, err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalog.Progress = func(progress Progress) {
		if progress.Source.Path != firstRoot {
			return
		}
		dir := filepath.Join(secondRoot, "main", "subagents")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent-new.jsonl"), []byte(claudeRecord("user", "main", "/work/z", "topic", "2026-01-03T00:00:00Z", "child prompt", "")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := catalog.List(t.Context(), ListQuery{Dirs: []string{"/work/z"}})
	if err != nil || len(got.Matches) != 0 {
		t.Fatalf("new child list=%#v, %v", got.Matches, err)
	}
}

// childFixture has a parent session with two child histories under its native
// child directory, a child with another session identity, and a child whose
// parent history is missing. Children differ from their parent in every
// filterable field.
func childFixture(t *testing.T) (Catalog, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"main.jsonl": claudeRecord("user", "main", "/work/a", "trunk", "2026-01-01T10:00:00Z", "deploy the service", "") +
			claudeRecord("assistant", "main", "/work/a", "trunk", "2026-01-01T10:01:00Z", "delegating the search", ""),
		"main/subagents/agent-1.jsonl": claudeRecord("user", "main", "/work/z", "topic", "2026-09-01T10:00:00Z", "the helper found a gremlin", ""),
		"main/subagents/agent-2.jsonl": claudeRecord("assistant", "main", "/work/z", "topic", "2026-09-01T10:05:00Z", "another gremlin near the secret", ""),
		"main/subagents/agent-3.jsonl": claudeRecord("user", "stray", "/work/z", "topic", "2026-09-02T10:00:00Z", "a gremlin of its own", ""),
		"lost/subagents/agent-4.jsonl": claudeRecord("user", "lost", "/work/z", "topic", "2026-09-03T10:00:00Z", "an orphaned gremlin", ""),
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Catalog{Sources: []Source{{Harness: registry.Harness("claude"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}, root
}

func TestChildHistoriesFoldIntoParentConversation(t *testing.T) {
	t.Parallel()
	c, root := childFixture(t)
	day := func(value string) time.Time {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	tests := []struct {
		name  string
		query Query
		want  []string
	}{
		{"child matches report the parent once", Query{Terms: []string{"gremlin"}}, []string{"lost", "main", "stray"}},
		{"terms may match parent and child", Query{Terms: []string{"deploy", "helper"}}, []string{"main"}},
		{"child exclusion drops the session", Query{Terms: []string{"gremlin"}, Exclude: []string{"secret"}}, []string{"lost", "stray"}},
		{"directory uses the parent", Query{Terms: []string{"gremlin"}, Dirs: []string{"/work/a"}}, []string{"main"}},
		{"branch uses the parent", Query{Terms: []string{"gremlin"}, GitBranch: "trunk"}, []string{"main"}},
		{"since uses the parent", Query{Terms: []string{"gremlin"}, Since: day("2026-06-01")}, []string{"lost", "stray"}},
		{"minimum messages uses the parent", Query{Terms: []string{"gremlin"}, MinMessages: 2}, []string{"main"}},
		{"limited search folds children", Query{Terms: []string{"gremlin"}, Limit: 1, Dirs: []string{"/work/a"}}, []string{"main"}},
	}
	for _, tt := range tests {
		got, err := c.Search(t.Context(), tt.query)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if tt.name == "child matches report the parent once" && (got.Refresh[0].Checked != 5 || got.Refresh[0].Reparsed != 5 || len(got.Reparsed) != 5) {
			t.Fatalf("folded file stats = %#v, files=%#v", got.Refresh, got.Reparsed)
		}
		if diff := cmp.Diff(tt.want, slices.Sorted(slices.Values(sessionIDs(got)))); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.name, diff)
		}
		compareIndexedSearch(t, c, tt.query)
	}

	requireFoldedExcerpts(t, c, root)
	requireFoldedList(t, c)
	appendRecord(t, filepath.Join(root, "main/subagents/agent-2.jsonl"), claudeRecord("assistant", "main", "/work/z", "topic", "2026-09-01T10:06:00Z", "a late zebra", ""))
	appended, err := c.Search(t.Context(), Query{Terms: []string{"zebra"}})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"main"}, sessionIDs(appended)); diff != "" {
		t.Fatalf("appended child (-want +got):\n%s", diff)
	}
}

func TestToolsSearchFoldsChildPartsAndReportsIssuesOnce(t *testing.T) {
	t.Parallel()
	catalog, root := childFixture(t)
	appendRecord(t, filepath.Join(root, "main/subagents/agent-1.jsonl"), `{"type":"user","sessionId":"main","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":"child-tool-marker"}]}}`+"\ninvalid record\n")
	query := Query{Terms: []string{"deploy", "child-tool-marker"}, IncludeTools: true, Dirs: []string{"/work/a"}, Limit: 1}
	compareIndexedSearch(t, catalog, query)
	result, err := catalog.Search(t.Context(), query)
	if err != nil || !slices.Equal(sessionIDs(result), []string{"main"}) || result.Matches[0].MatchingParts != 2 || len(result.Issues) != 1 || !result.Issues[0].Record {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.Matches[0].Excerpts[1].Role != "tool" || result.Matches[0].Excerpts[1].Text != "child-tool-marker" {
		t.Fatalf("excerpts=%#v", result.Matches[0].Excerpts)
	}
	query.Exclude = []string{"secret"}
	compareIndexedSearch(t, catalog, query)
	result, err = catalog.Search(t.Context(), query)
	if err != nil || len(result.Matches) != 0 || len(result.Issues) != 0 || result.OmittedIssues != 0 {
		t.Fatalf("excluded result=%#v err=%v", result, err)
	}
	requireUnreadableToolChild(t, catalog, root)
}

func TestToolsPrefilterSkipsDiagnostics(t *testing.T) {
	t.Parallel()
	for _, family := range []bool{false, true} {
		t.Run(strconv.FormatBool(family), func(t *testing.T) {
			catalog := malformedIssueFixture(t)
			count := 2
			if family {
				var root string
				catalog, root = childFixture(t)
				count = 5
				appendRecord(t, filepath.Join(root, "main.jsonl"), "{broken\n")
			}
			requireToolsSkippedDiagnostics(t, catalog, count)
		})
	}
}

func TestToolsUnknownFamilyFileReportsFailure(t *testing.T) {
	t.Parallel()
	catalog, root := childFixture(t)
	path := filepath.Join(root, "main/subagents/agent-unknown.jsonl")
	if err := os.WriteFile(path, []byte("{\"unknown\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, warm := range []bool{false, true} {
		if warm {
			if _, err := catalog.Refresh(t.Context()); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("refresh=%v", err)
			}
		}
		query := Query{Terms: []string{"absent"}, IncludeTools: true}
		compareIndexedSearch(t, catalog, query)
		result, err := catalog.Search(t.Context(), query)
		if !errors.Is(err, ErrIncomplete) || len(result.Issues) != 1 || result.Issues[0].Record || result.Issues[0].Path != path {
			t.Fatalf("warm=%t result=%#v err=%v", warm, result, err)
		}
	}
}

func requireToolsSkippedDiagnostics(t *testing.T, catalog Catalog, count int) {
	t.Helper()
	for _, warm := range []bool{false, true} {
		if warm {
			if _, err := catalog.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []Query{
			{Terms: []string{"absent"}, IncludeTools: true},
			{Terms: []string{"broken"}, IncludeTools: true, Since: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		} {
			compareIndexedSearch(t, catalog, query)
			result, err := catalog.Search(t.Context(), query)
			if err != nil || len(result.Issues) != 0 || result.OmittedIssues != 0 || len(result.Reparsed) != 0 {
				t.Fatalf("warm=%t query=%#v result=%#v err=%v", warm, query, result, err)
			}
			if warm {
				requireUnchangedRefresh(t, result, count)
			}
		}
	}
}

func requireUnreadableToolChild(t *testing.T, catalog Catalog, root string) {
	t.Helper()
	path := filepath.Join(root, "main/subagents/agent-unreadable.jsonl")
	if err := os.WriteFile(path, []byte("unreadable"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("child is readable: %v", err)
	}
	query := Query{Terms: []string{"absent"}, IncludeTools: true, Dirs: []string{"/work/a"}}
	compareIndexedSearch(t, catalog, query)
	result, err := catalog.Search(t.Context(), query)
	if !errors.Is(err, ErrIncomplete) || len(result.Issues) != 1 || len(result.Matches) != 0 {
		t.Fatalf("unreadable child result=%#v err=%v", result, err)
	}
}

func requireFoldedExcerpts(t *testing.T, c Catalog, root string) {
	t.Helper()
	got, err := c.Search(t.Context(), Query{Terms: []string{"the"}, Dirs: []string{"/work/a"}})
	if err != nil || len(got.Matches) != 1 {
		t.Fatalf("search = %#v, %v", got, err)
	}
	match := got.Matches[0]
	if match.Conversation.Path != filepath.Join(root, "main.jsonl") || match.Conversation.CWD != "/work/a" || match.MatchingParts != 4 {
		t.Fatalf("folded conversation = %#v", match)
	}
	excerpts := make([]string, 0, len(match.Excerpts))
	for _, excerpt := range match.Excerpts {
		excerpts = append(excerpts, excerpt.Text)
	}
	if diff := cmp.Diff([]string{"deploy the service", "delegating the search", "the helper found a gremlin"}, excerpts); diff != "" {
		t.Fatalf("excerpts (-want +got):\n%s", diff)
	}
}

func requireFoldedList(t *testing.T, c Catalog) {
	t.Helper()
	listed, err := c.List(t.Context(), ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"lost", "main", "stray"}, slices.Sorted(slices.Values(sessionIDs(listed)))); diff != "" {
		t.Fatalf("list (-want +got):\n%s", diff)
	}
	direct, err := c.listDirect(t.Context(), ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(direct, listed, cmpopts.IgnoreFields(Result{}, "Refresh", "Reparsed", "IndexUnavailable")); diff != "" {
		t.Fatalf("list (-direct +index):\n%s", diff)
	}
}

func appendRecord(t *testing.T, path, record string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(record); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPresenceFilterUsesRegistryEvidence(t *testing.T) {
	t.Parallel()
	c := filterFixture(t)
	incarnation := func(id, session string, presence registry.Presence) registry.Session {
		return registry.Session{ID: id, Harness: registry.Harness("claude"), SessionID: session, Liveness: registry.NewLiveness(presence, registry.ActivityValue(nil), nil)}
	}
	sessions := []registry.Session{
		incarnation("r1", "new", registry.PresenceLive),
		incarnation("r2", "old", registry.PresenceGone),
		incarnation("r3", "mid", registry.PresenceGone),
		incarnation("r4", "mid", registry.PresenceLive),
	}
	for _, tt := range []struct {
		presence registry.Presence
		want     []string
	}{
		{registry.PresenceLive, []string{"mid", "new"}},
		{registry.PresenceGone, []string{"old"}},
	} {
		filter := Filter{Presence: tt.presence, Registry: sessions}
		listed, err := c.List(t.Context(), ListQuery{Filter: filter, Sort: "", Limit: 0})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(tt.want, slices.Sorted(slices.Values(sessionIDs(listed)))); diff != "" {
			t.Errorf("list %s (-want +got):\n%s", tt.presence, diff)
		}
		q := Query{Terms: []string{"e"}, Presence: tt.presence, Registry: sessions}
		found, err := c.Search(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(tt.want, slices.Sorted(slices.Values(sessionIDs(found)))); diff != "" {
			t.Errorf("search %s (-want +got):\n%s", tt.presence, diff)
		}
		compareIndexedSearch(t, c, q)
	}
}

func TestTimeBoundsUseUndatedTranscriptModificationTime(t *testing.T) {
	t.Parallel()
	c := filterFixture(t)
	writeIndexFixture(t, c, "undated.jsonl", claudeRecord("user", "undated", "/work/a", "main", "", "an undated token", ""))
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	modified := day.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(c.Sources[0].Path, "undated.jsonl"), modified, modified); err != nil {
		t.Fatal(err)
	}
	for _, q := range []Query{
		{Terms: []string{"token"}},
		{Terms: []string{"token"}, Since: day},
		{Terms: []string{"token"}, Until: day},
	} {
		compareIndexedSearch(t, c, q)
		got, err := c.Search(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		undated := slices.Contains(sessionIDs(got), "undated")
		if undated != q.Until.IsZero() {
			t.Fatalf("query %#v returned %v", q, sessionIDs(got))
		}
	}
}

func TestExistingUntimestampedIndexAcquiresActivityFallback(t *testing.T) {
	t.Parallel()
	catalog := filterFixture(t)
	writeIndexFixture(t, catalog, "undated.jsonl", claudeRecord("user", "undated", "/work/a", "main", "", "an undated token", ""))
	modified := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(catalog.Sources[0].Path, "undated.jsonl"), modified, modified); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.List(t.Context(), ListQuery{}); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", catalog.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := database.ExecContext(t.Context(), "UPDATE conversations SET metadata=json_remove(metadata,'$.updated_at'),active=?,started=? WHERE metadata LIKE '%undated%'; PRAGMA user_version=5", unknownActive, unknownStarted)
	if err := errors.Join(updateErr, database.Close()); err != nil {
		t.Fatal(err)
	}
	query := Query{Terms: []string{"token"}, Since: modified.Add(-time.Hour)}
	result, err := catalog.Search(t.Context(), query)
	if err != nil || !slices.Equal(sessionIDs(result), []string{"undated"}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	compareIndexedSearch(t, catalog, query)
}

func writeIndexFixture(t *testing.T, c Catalog, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.Sources[0].Path, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStaleAppendResumeReparsesWholeHistory(t *testing.T) {
	t.Parallel()
	c := filterFixture(t)
	if _, err := c.Search(t.Context(), Query{Terms: []string{"token"}}); err != nil {
		t.Fatal(err)
	}
	index, err := openHistoryIndex(t.Context(), c.IndexPath, c.Sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	source := c.Sources[0]
	path := filepath.Join(source.Path, "new.jsonl")
	appendRecord(t, path, claudeRecord("user", "new", "/work/b", "feature", "2026-09-01T10:03:00Z", "first appended walrus", ""))
	s := &search{index: index, mode: modeRefresh}
	s.reset()
	file, ok := s.singleFile(source, path)
	if !ok {
		t.Fatal("history file was not selected")
	}
	job := index.newJob(s, source, file)
	job.done <- parseRefresh(t.Context(), job)

	appendRecord(t, path, claudeRecord("assistant", "new", "/work/b", "feature", "2026-09-01T10:04:00Z", "second appended walrus", ""))
	if _, err := c.Search(t.Context(), Query{Terms: []string{"walrus"}}); err != nil {
		t.Fatal(err)
	}

	var status SourceStatus
	var progress Progress
	batch := new(refreshBatch)
	index.finish(t.Context(), s, source, pendingFile{file: file, filter: s.query.Filter, job: job, children: nil}, &status, &progress, batch)
	index.flushRefresh(t.Context(), s, batch, &progress)
	if s.indexErr != nil || len(s.result.Issues) != 0 {
		t.Fatalf("stale resume: %v %#v", s.indexErr, s.result.Issues)
	}
	got, err := c.Search(t.Context(), Query{Terms: []string{"walrus"}})
	if err != nil || len(got.Matches) != 1 || got.Matches[0].MatchingParts != 2 {
		t.Fatalf("search after stale resume = %#v, %v", got, err)
	}
	compareIndexedSearch(t, c, Query{Terms: []string{"walrus"}})
	compareIndexedSearch(t, c, Query{Terms: []string{"token"}})
}

func TestScopedIndexedSearchMatchesDirectSearch(t *testing.T) {
	t.Parallel()
	catalog := parityFixture(t)
	root := catalog.Sources[0].Path
	catalog.Sources = append(catalog.Sources, Source{Harness: registry.Harness("omp"), Path: root})
	if _, err := catalog.Search(t.Context(), Query{Terms: []string{"needle"}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		sources []Source
		query   Query
	}{
		{"agent", catalog.Sources, Query{Terms: []string{"needle"}, Harnesses: []registry.Harness{registry.Harness("pi")}}},
		{"file", []Source{{Harness: registry.Harness("pi"), Path: filepath.Join(root, "0.jsonl")}}, Query{Terms: []string{"needle"}}},
		{"ignored", catalog.Sources, Query{Terms: []string{"needle"}, IgnoreHarnesses: []registry.Harness{registry.Harness("pi")}}},
		{"none", catalog.Sources, Query{Terms: []string{"needle"}, Harnesses: []registry.Harness{registry.Harness("codex")}}},
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
	query := Query{Terms: []string{"rare target phrase"}, Limit: 1}
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
		result, err := catalog.Search(b.Context(), Query{Terms: []string{"needle"}})
		if err != nil || len(result.Matches) != 1 {
			b.Fatalf("search = %d matches, %v", len(result.Matches), err)
		}
	}
}
