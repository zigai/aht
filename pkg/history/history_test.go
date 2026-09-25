package history_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

const treeHistory = `{"type":"session","version":3,"id":"native-session","cwd":"/work/project/sub","timestamp":"2026-09-01T00:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-01T00:00:01Z","message":{"role":"user","content":"Find the Refresh Token handler"}}
{"type":"message","id":"a1","timestamp":"2026-09-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning-only"},{"type":"text","text":"The refresh token handler is in auth.go"},{"type":"toolCall","name":"read","arguments":{"file":"tool-only"}}]}}
{"type":"message","id":"t1","message":{"role":"toolResult","content":[{"type":"text","text":"tool-only output"}]}}
{"type":"message","id":"s1","message":{"role":"system","content":"system-only"}}
{"type":"session_info","name":"Authentication work"}
`

// toolHistory has a tool message newer than its searchable text, so searching
// with tools can only change excerpts, never conversation metadata.
const toolHistory = `{"type":"session","id":"tool-session","cwd":"/work/tools","timestamp":"2026-09-01T10:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":"refresh token"}}
{"type":"message","id":"a1","timestamp":"2026-09-01T10:02:00Z","message":{"role":"assistant","content":"refresh token handler"}}
{"type":"message","id":"t1","timestamp":"2026-09-01T10:03:00Z","message":{"role":"toolResult","content":[{"type":"text","text":"tool-only output"}]}}
`

func writeHistory(t *testing.T, root, name, body string) string {
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

func searchFile(t *testing.T, h registry.Harness, name, body, text string, tools bool) history.Result {
	t.Helper()
	path := writeHistory(t, t.TempDir(), name, body)
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: h, Path: path}}}
	result, err := c.Search(t.Context(), history.Query{Text: text, IncludeTools: tools})
	if err != nil {
		t.Fatalf("search failed: %v, %#v", err, result.Issues)
	}
	return result
}

func TestNativeJSONLReaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		harness    registry.Harness
		file, body string
	}{
		{"pi", registry.Harness("pi"), "session.jsonl", treeHistory},
		{"omp", registry.Harness("omp"), "session.jsonl", strings.ReplaceAll(treeHistory, `{"type":"session_info","name":"Authentication work"}`, `{"type":"title_change","title":"Authentication work"}`)},
		{"openclaw", registry.Harness("openclaw"), "session.jsonl", treeHistory},
		{"claude", registry.Harness("claude"), "session.jsonl", `{"type":"user","sessionId":"native-session","cwd":"/work/project","uuid":"u1","message":{"role":"user","content":"Refresh Token"}}
{"type":"user","sessionId":"native-session","message":{"role":"user","content":[{"type":"tool_result","content":"tool-only"}]}}
{"type":"assistant","sessionId":"native-session","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning-only"}]}}
`},
		{"codex", registry.Harness("codex"), "rollout-session.jsonl", `{"type":"session_meta","payload":{"id":"native-session","cwd":"/work/project"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Refresh Token"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"Refresh Token"}}
{"type":"response_item","payload":{"type":"function_call_output","call_id":"t1","output":"tool-only"}}
{"type":"response_item","payload":{"type":"reasoning","summary":[{"text":"reasoning-only"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"analysis","content":[{"type":"output_text","text":"reasoning-only"}]}}
`},
		{"copilot", registry.Harness("copilot"), "events.jsonl", `{"type":"session.start","data":{"sessionId":"native-session","context":{"cwd":"/work/project"}}}
{"type":"user.message","id":"u1","data":{"content":"Refresh Token"}}
{"type":"assistant.message_delta","data":{"deltaContent":"Refresh Token"}}
{"type":"tool.execution_complete","data":{"result":{"content":"tool-only"}}}
{"type":"assistant.reasoning","data":{"content":"reasoning-only"}}
`},
		{"kimi", registry.Harness("kimi-code"), "native-session/context.jsonl", `{"role":"user","content":[{"type":"text","text":"Refresh Token"}]}
{"role":"tool","content":"tool-only"}
{"role":"assistant","content":[{"type":"think","think":"reasoning-only"}]}
`},
		{"cline", registry.Harness("cline"), "native-session.messages.json", `{"version":1,"sessionId":"native-session","system_prompt":"system-only","messages":[{"id":"u1","role":"user","content":"Refresh Token"},{"role":"tool","content":[{"type":"tool-result","output":{"value":"tool-only"}}]},{"role":"assistant","content":[{"type":"reasoning","text":"reasoning-only"}]}]}`},
		{"amp", registry.Harness("amp"), "T-native-session.json", `{"v":1,"id":"native-session","title":"Authentication work","env":{"initial":{"trees":[{"uri":"file:///work/project"}]}},"messages":[{"messageId":0,"role":"user","content":[{"type":"text","text":"Refresh Token"}]},{"messageId":1,"role":"tool","content":[{"type":"tool-result","output":"tool-only"}]},{"messageId":2,"role":"assistant","content":[{"type":"thinking","thinking":"reasoning-only"}]},{"messageId":3,"role":"system","content":"system-only"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertNativeTextPolicy(t, tt.harness, tt.file, tt.body)
		})
	}
}

func TestSearchFiltersMetadataAndRegistryJoin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := writeHistory(t, root, "session.jsonl", treeHistory)
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}}
	q := history.Query{Text: "refresh token", Dir: "/work/project", Registry: []registry.Session{
		{
			ID:          "live",
			Harness:     registry.Harness("pi"),
			SessionID:   "native-session",
			SessionPath: path,
			UpdatedAt:   time.Now(),
			Liveness:    registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:        "wrong-harness",
			Harness:   registry.Harness("codex"),
			SessionID: "native-session",
			Liveness:  registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
		{
			ID:          "wrong-profile",
			Harness:     registry.Harness("pi"),
			SessionID:   "native-session",
			SessionPath: "/other/session.jsonl",
			Liveness:    registry.NewLiveness(registry.PresenceLive, registry.ActivityValue(nil), nil),
		},
	}}
	result, err := c.Search(t.Context(), q)
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("search = %#v, %v", result, err)
	}
	m := result.Matches[0]
	if m.Conversation.Title != "Authentication work" || m.Conversation.CreatedAt.IsZero() || len(m.Live) != 1 || m.Live[0].RegistryID != "live" {
		t.Fatalf("metadata = %#v", m)
	}
	q.Dir = "/work/proj"
	result, err = c.Search(t.Context(), q)
	if err != nil || len(result.Matches) != 0 {
		t.Fatal("directory filter matched a non-directory prefix")
	}
	q.Dir = ""
	q.CaseSensitive = true
	q.Text = "REFRESH TOKEN"
	result, err = c.Search(t.Context(), q)
	if err != nil || len(result.Matches) != 0 {
		t.Fatal("case-sensitive search ignored case")
	}
}

func TestPartialSearchAndLimits(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "a.jsonl", treeHistory+"invalid private-content\n")
	writeHistory(t, root, "b.jsonl", strings.ReplaceAll(treeHistory, "native-session", "other-session"))
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}}
	result, err := c.Search(t.Context(), history.Query{Text: "refresh", Limit: 1})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 1 || !result.Truncated || len(result.Issues) != 1 {
		t.Fatalf("partial = %#v, %v", result, err)
	}
	if strings.Contains(result.Issues[0].Message, "private-content") {
		t.Fatal("malformed transcript echoed in diagnostic")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Search(ctx, history.Query{Text: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, q := range []history.Query{{}, {Text: "x", Limit: -1}} {
		if _, err := c.Search(t.Context(), q); !errors.Is(err, history.ErrInvalidQuery) {
			t.Fatalf("invalid query accepted: %#v", q)
		}
	}
}

func TestSearchOptionalLimit(t *testing.T) {
	t.Parallel()
	const conversations = 1002
	root := t.TempDir()
	for i := range conversations {
		id := strconv.Itoa(i)
		writeHistory(t, root, id+".jsonl", strings.ReplaceAll(treeHistory, "native-session", id))
	}
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}}
	for _, limit := range []int{0, 50, 1001, conversations, 2000} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			result, err := c.Search(t.Context(), history.Query{Text: "refresh", Limit: limit})
			want := conversations
			if limit > 0 {
				want = min(want, limit)
			}
			if err != nil || len(result.Matches) != want || result.Truncated != (want < conversations) {
				t.Fatalf("limit %d: matches=%d truncated=%t err=%v", limit, len(result.Matches), result.Truncated, err)
			}
		})
	}
}

func TestMissingUnsupportedAndSymlinkSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := writeHistory(t, t.TempDir(), "session.jsonl", treeHistory)
	if err := os.Symlink(outside, filepath.Join(root, "outside.jsonl")); err != nil {
		t.Fatal(err)
	}
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}, {Harness: registry.Harness("codex"), Path: filepath.Join(root, "absent")}, {Harness: registry.Harness("cursor"), Path: root}}}
	result, err := c.Search(t.Context(), history.Query{Text: "refresh"})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 || result.Sources[1].Status != "missing" || result.Sources[2].Status != "unsupported" {
		t.Fatalf("coverage = %#v, %v", result, err)
	}
	if _, err := c.Search(t.Context(), history.Query{Text: "refresh", Harness: registry.Harness("cursor")}); !errors.Is(err, history.ErrIncomplete) {
		t.Fatalf("explicit unsupported search = %v", err)
	}
}

func TestUnicodeExcerptAndJSON(t *testing.T) {
	t.Parallel()
	body := strings.Replace(treeHistory, "Find the Refresh Token handler", strings.Repeat("é", 300)+" İSTANBUL refresh token "+strings.Repeat("é", 300), 1)
	result := searchFile(t, registry.Harness("pi"), "session.jsonl", body, "istanbul", false)
	if len(result.Matches) != 1 || !strings.Contains(result.Matches[0].Excerpts[0].Text, "İSTANBUL") {
		t.Fatalf("Unicode match = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestKimiNativeDirectoryMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "kimi.json", `{"work_dirs":[{"path":"/work/kimi-project","kaos":"local"}]}`)
	// MD5('/work/kimi-project') is the directory key in Kimi's native metadata format.
	path := writeHistory(t, root, "sessions/aaec326b87de6c65cbc919cff0fa048e/native/context.jsonl", `{"role":"user","content":"refresh token"}`)
	link := filepath.Join(t.TempDir(), "context.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, path string }{
		{"directory", filepath.Join(root, "sessions")},
		{"file", path},
		{"symlink", link},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("kimi-code"), Path: tt.path}}}
			result, err := catalog.Search(t.Context(), history.Query{Text: "refresh", Dir: "/work/kimi-project"})
			if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.CWD != "/work/kimi-project" {
				t.Fatalf("Kimi cwd = %#v, %v", result, err)
			}
			result, err = catalog.Search(t.Context(), history.Query{Text: "refresh", IgnorePaths: []string{"/work/kimi-project"}})
			if err != nil || len(result.Matches) != 0 {
				t.Fatalf("ignored Kimi cwd = %#v, %v", result, err)
			}
		})
	}
}

func assertNativeTextPolicy(t *testing.T, h registry.Harness, file, body string) {
	t.Helper()
	result := searchFile(t, h, file, body, "refresh token", false)
	if len(result.Matches) != 1 || result.Matches[0].Conversation.SessionID != "native-session" {
		t.Fatalf("matches = %#v", result.Matches)
	}
	if (h == registry.Harness("omp") || h == registry.Harness("pi")) && result.Matches[0].Conversation.Title != "Authentication work" {
		t.Fatalf("%s conversation title = %q, want %q", h, result.Matches[0].Conversation.Title, "Authentication work")
	}
	if result.Matches[0].Live == nil || len(result.Matches[0].Live) != 0 {
		t.Fatal("untracked historical session should have unknown presence")
	}
	if h == registry.Harness("codex") && result.Matches[0].MatchingParts != 1 {
		t.Fatal("duplicated mirrored Codex event")
	}
	assertRoleFilteringPolicy(t, h, file, body)
}

func assertRoleFilteringPolicy(t *testing.T, h registry.Harness, file, body string) {
	t.Helper()
	for _, text := range []string{"tool-only", "reasoning-only", "system-only"} {
		if got := searchFile(t, h, file, body, text, false); len(got.Matches) != 0 {
			t.Fatalf("default search exposed %s", text)
		}
	}
	if got := searchFile(t, h, file, body, "tool-only", true); len(got.Matches) != 1 || got.Matches[0].Excerpts[0].Role != "tool" {
		t.Fatalf("tool opt-in = %#v", got.Matches)
	}
	if got := searchFile(t, h, file, body, "reasoning-only", true); len(got.Matches) != 0 {
		t.Fatal("tool opt-in exposed reasoning")
	}
}

func TestClineManifestAndConfiguredPathFiltering(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "native.messages.json", `{"version":1,"sessionId":"native","updated_at":"2026-09-01T00:00:00Z","messages":[{"role":"user","content":"refresh token"}]}`)
	writeHistory(t, root, "native.json", `{"session_id":"native","cwd":"/work/private/project","workspace_root":"/work/private","metadata":{"title":"Native title"},"messages_path":"/outside/do-not-follow"}`)
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("cline"), Path: root}}}
	result, err := c.Search(t.Context(), history.Query{Text: "refresh", Dir: "/work"})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.Title != "Native title" {
		t.Fatalf("manifest = %#v, %v", result, err)
	}
	result, err = c.Search(t.Context(), history.Query{Text: "refresh", IgnorePaths: []string{"**/private/**"}})
	if err != nil || len(result.Matches) != 0 {
		t.Fatalf("ignored path = %#v, %v", result, err)
	}
}

func TestOversizedRecordDoesNotHideLaterMessages(t *testing.T) {
	t.Parallel()
	const recordBudget = 16 << 20
	body := `{"type":"session","id":"large"}` + "\n" + `{"type":"blob","data":"` + strings.Repeat("x", recordBudget) + `"}` + "\n" + `{"type":"message","message":{"role":"user","content":"after-big-record"}}` + "\n"
	path := writeHistory(t, t.TempDir(), "large.jsonl", body)
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("pi"), Path: path}}}
	result, err := c.Search(t.Context(), history.Query{Text: "after-big-record"})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 1 || len(result.Issues) != 1 {
		t.Fatalf("oversized record = %#v, %v", result, err)
	}
}

func TestOpenClawRetainedArchives(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "main/sessions/native.jsonl.deleted.2026-09-01", treeHistory)
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("openclaw"), Path: root}}}
	result, err := c.Search(t.Context(), history.Query{Text: "refresh"})
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("archive search = %#v, %v", result, err)
	}
	writeHistory(t, root, "main/sessions/other.jsonl.reset.2026-09-01.zst", "compressed-placeholder")
	result, err = c.Search(t.Context(), history.Query{Text: "refresh"})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 1 || !strings.Contains(result.Issues[0].Message, "compressed") {
		t.Fatalf("compressed coverage = %#v, %v", result, err)
	}
}

func TestCopilotToolRequestsAreOptIn(t *testing.T) {
	t.Parallel()
	const body = `{"type":"session.start","data":{"sessionId":"native"}}
{"type":"assistant.message","data":{"content":"Checking","toolRequests":[{"name":"bash","arguments":{"command":"request-only"},"toolCallId":"call"}]}}
`
	if result := searchFile(t, registry.Harness("copilot"), "events.jsonl", body, "request-only", false); len(result.Matches) != 0 {
		t.Fatal("tool request leaked into default search")
	}
	if result := searchFile(t, registry.Harness("copilot"), "events.jsonl", body, "request-only", true); len(result.Matches) != 1 {
		t.Fatal("opt-in did not search tool request")
	}
}

// directCatalog returns a catalog whose index path cannot be created, so the
// public API takes the production direct-scan path.
func directCatalog(t *testing.T, sources []history.Source) history.Catalog {
	t.Helper()
	blocker := writeHistory(t, t.TempDir(), "not-a-directory", "an index cannot live under a regular file\n")
	return history.Catalog{Sources: sources, IndexPath: filepath.Join(blocker, "index.sqlite")}
}

// searchMode pairs one production search path with its name, so a behavior can be
// asserted through both the disposable index and the direct scan.
type searchMode struct {
	name    string
	catalog history.Catalog
}

func searchModes(t *testing.T, sources []history.Source) []searchMode {
	t.Helper()
	return []searchMode{
		{name: "indexed", catalog: history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: sources}},
		{name: "direct", catalog: directCatalog(t, sources)},
	}
}

// requireMatches asserts that a search returned want matching conversations and
// the expected error.
func requireMatches(t *testing.T, result history.Result, err error, want int) {
	t.Helper()
	if err != nil || len(result.Matches) != want {
		t.Fatalf("search = %d matches, %v", len(result.Matches), err)
	}
}

func TestUnusableIndexPathFallsBackToDirectScan(t *testing.T) {
	t.Parallel()
	path := writeHistory(t, t.TempDir(), "session.jsonl", treeHistory)
	catalog := directCatalog(t, []history.Source{{Harness: registry.Harness("pi"), Path: path}})
	result, err := catalog.Search(t.Context(), history.Query{Text: "refresh token"})
	requireMatches(t, result, err, 1)
	if result.Matches[0].Conversation.SessionID != "native-session" {
		t.Fatalf("fallback session = %q", result.Matches[0].Conversation.SessionID)
	}
	if len(result.Sources) != 1 || result.Sources[0].Status != "searched" || len(result.Issues) != 0 {
		t.Fatalf("fallback coverage = %#v", result.Sources)
	}
}

func TestDuplicateSourcesAreDeduplicated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := writeHistory(t, root, "session.jsonl", treeHistory)
	// The second spelling reaches the same file through an unclean path, so
	// deduplication has to canonicalize instead of comparing source strings.
	duplicate := root + string(filepath.Separator) + "." + string(filepath.Separator) + "session.jsonl"
	catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{
		{Harness: registry.Harness("pi"), Path: path},
		{Harness: registry.Harness("pi"), Path: duplicate},
	}}
	result, err := catalog.Search(t.Context(), history.Query{Text: "refresh"})
	requireMatches(t, result, err, 1)
	if len(result.Sources) != 1 || result.Sources[0].Status != "searched" {
		t.Fatalf("duplicate source coverage = %#v", result.Sources)
	}
	limited, err := catalog.Search(t.Context(), history.Query{Text: "refresh", Limit: 1})
	requireMatches(t, limited, err, 1)
	if limited.Truncated {
		t.Fatal("duplicate source consumed the result limit")
	}
}

func TestSkippedSourcesReportIncompleteCoverage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "session.jsonl", treeHistory)
	catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{
		{Harness: registry.Harness("pi"), Path: root},
		{Harness: registry.Harness("codex"), Path: root},
	}}
	for _, tt := range []struct {
		name   string
		query  history.Query
		reason string
	}{
		{
			name:   "harness filter",
			query:  history.Query{Text: "refresh", Harness: registry.Harness("cursor")},
			reason: "does not match the requested harness",
		},
		{
			name:   "ignore list",
			query:  history.Query{Text: "refresh", IgnoreHarnesses: []registry.Harness{registry.Harness("pi"), registry.Harness("codex")}},
			reason: "excluded by the ignore list",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := catalog.Search(t.Context(), tt.query)
			if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 {
				t.Fatalf("uncovered search = %#v, %v", result, err)
			}
			if len(result.Sources) != 2 || len(result.Issues) != 2 {
				t.Fatalf("uncovered coverage = %#v", result)
			}
			for _, status := range result.Sources {
				if status.Status != "skipped" {
					t.Fatalf("uncovered coverage = %#v", result.Sources)
				}
			}
			if !strings.Contains(result.Issues[0].Message, tt.reason) {
				t.Fatalf("skip reason = %q, want %q", result.Issues[0].Message, tt.reason)
			}
		})
	}
}

func TestPartiallySkippedSourcesAreNotIncomplete(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "session.jsonl", treeHistory)
	catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{
		{Harness: registry.Harness("pi"), Path: root},
		{Harness: registry.Harness("codex"), Path: root},
	}}
	result, err := catalog.Search(t.Context(), history.Query{Text: "refresh", Harness: registry.Harness("pi")})
	requireMatches(t, result, err, 1)
	if len(result.Issues) != 0 {
		t.Fatalf("partial coverage issues = %#v", result.Issues)
	}
	if len(result.Sources) != 2 || result.Sources[0].Status != "searched" || result.Sources[1].Status != "skipped" {
		t.Fatalf("partial coverage statuses = %#v", result.Sources)
	}
}

func TestConversationMetadataIndependentOfToolSearch(t *testing.T) {
	t.Parallel()
	path := writeHistory(t, t.TempDir(), "session.jsonl", toolHistory)
	sources := []history.Source{{Harness: registry.Harness("pi"), Path: path}}
	for _, mode := range searchModes(t, sources) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			plain, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token"})
			requireMatches(t, plain, err, 1)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token", IncludeTools: true})
			requireMatches(t, tools, err, 1)
			if diff := cmp.Diff(plain.Matches[0].Conversation, tools.Matches[0].Conversation); diff != "" {
				t.Fatalf("conversation metadata depends on IncludeTools (-plain +tools):\n%s", diff)
			}
			if got := plain.Matches[0].Conversation.UpdatedAt.UTC().Format(time.RFC3339); got != "2026-09-01T10:03:00Z" {
				t.Fatalf("conversation UpdatedAt = %s, want the tool message timestamp", got)
			}
			if got := plain.Matches[0].Conversation.Title; got != "refresh token" {
				t.Fatalf("conversation Title = %q, want the first user message", got)
			}
		})
	}
}

func TestToolExcerptsAreOptIn(t *testing.T) {
	t.Parallel()
	path := writeHistory(t, t.TempDir(), "session.jsonl", toolHistory)
	sources := []history.Source{{Harness: registry.Harness("pi"), Path: path}}
	for _, mode := range searchModes(t, sources) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			plain, err := mode.catalog.Search(t.Context(), history.Query{Text: "tool-only"})
			requireMatches(t, plain, err, 0)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Text: "tool-only", IncludeTools: true})
			requireMatches(t, tools, err, 1)
			if tools.Matches[0].Excerpts[0].Role != "tool" {
				t.Fatalf("tool excerpt role = %q", tools.Matches[0].Excerpts[0].Role)
			}
		})
	}
}

// orderingSources writes two matching conversations whose creation order and
// update order disagree: the older conversation was updated last. The newer
// creation is discovered first, so only the frozen ordering may decide.
func orderingSources(t *testing.T) []history.Source {
	t.Helper()
	const recentlyUpdated = `{"type":"session","id":"recently-updated","cwd":"/work/project","timestamp":"2026-09-19T00:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-19T00:00:01Z","message":{"role":"user","content":"refresh token"}}
{"type":"message","id":"a1","timestamp":"2026-09-20T09:00:00Z","message":{"role":"assistant","content":"refresh token again"}}
`
	const recentlyCreated = `{"type":"session","id":"recently-created","cwd":"/work/project","timestamp":"2026-09-20T08:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-20T08:00:01Z","message":{"role":"user","content":"refresh token"}}
{"type":"message","id":"a1","timestamp":"2026-09-20T08:30:00Z","message":{"role":"assistant","content":"refresh token again"}}
`
	root := t.TempDir()
	created := writeHistory(t, root, "a-recently-created.jsonl", recentlyCreated)
	updated := writeHistory(t, root, "b-recently-updated.jsonl", recentlyUpdated)
	return []history.Source{{Harness: registry.Harness("pi"), Path: created}, {Harness: registry.Harness("pi"), Path: updated}}
}

func TestResultOrderingPrefersMostRecentlyUpdated(t *testing.T) {
	t.Parallel()
	for _, mode := range searchModes(t, orderingSources(t)) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			result, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token"})
			requireMatches(t, result, err, 2)
			order := []string{result.Matches[0].Conversation.SessionID, result.Matches[1].Conversation.SessionID}
			if order[0] != "recently-updated" || order[1] != "recently-created" {
				t.Fatalf("unlimited order = %v", order)
			}
		})
	}
}

func TestResultLimitKeepsTheMostRecentlyUpdated(t *testing.T) {
	t.Parallel()
	for _, mode := range searchModes(t, orderingSources(t)) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			limited, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token", Limit: 1})
			requireMatches(t, limited, err, 1)
			if limited.Matches[0].Conversation.SessionID != "recently-updated" || !limited.Truncated {
				t.Fatalf("limited search = %#v", limited.Matches)
			}
			exact, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token", Limit: 2})
			requireMatches(t, exact, err, 2)
			if exact.Truncated {
				t.Fatal("exact limit reported truncation")
			}
		})
	}
}
