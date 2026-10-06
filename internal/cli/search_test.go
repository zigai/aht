package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

// searchCLIHome isolates HOME and the cache directory, returning HOME so tests
// can place histories at their default discovery locations.
func searchCLIHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", home)
	return home
}

func writeSearchFixture(t *testing.T, path string) {
	t.Helper()
	const body = `{"type":"session","id":"native-id","cwd":"/work/project","title":"Search example"}
{"type":"message","id":"u1","message":{"role":"user","content":"Refresh token\u001b[31m\u202e"}}
{"type":"message","id":"t1","message":{"role":"toolResult","content":"tool-only"}}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func searchCLIFixture(t *testing.T) string {
	t.Helper()
	searchCLIHome(t)
	path := filepath.Join(t.TempDir(), "history.jsonl")
	writeSearchFixture(t, path)
	return path
}

// searchSourceStatus returns the coverage status reported for one source.
func searchSourceStatus(t *testing.T, result history.Result, harness registry.Harness, path string) string {
	t.Helper()
	for _, status := range result.Sources {
		if status.Source.Harness == harness && filepath.Clean(status.Source.Path) == filepath.Clean(path) {
			return status.Status
		}
	}
	t.Fatalf("no coverage for %s %s: %#v", harness, path, result.Sources)
	return ""
}

func TestSearchCLIJSONAndToolOptIn(t *testing.T) {
	path := searchCLIFixture(t)
	store := filepath.Join(t.TempDir(), "state.json")
	for _, tools := range []bool{false, true} {
		args := []string{"--no-config", "--store", store, "--json", "search", "tool-only", "--source", "pi=" + path, "--dir", "/work"}
		if tools {
			args = append(args, "--include-tools")
		}
		var stdout, stderr bytes.Buffer
		if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		var result history.Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if (len(result.Matches) == 1) != tools {
			t.Fatalf("tool selection = %#v", result.Matches)
		}
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("history search created a registry: %v", err)
	}
}

func TestSearchCLIRendersSafeTextAndPartialJSON(t *testing.T) {
	path := searchCLIFixture(t)
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "search", "refresh token", "--source", "pi=" + path, "--json=false"}
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stdout.String(), "\x1b\u202e") || !strings.Contains(stdout.String(), "native-id") {
		t.Fatalf("unsafe output %q", stdout.String())
	}
	appendMalformedRecord(t, path)
	stdout.Reset()
	stderr.Reset()
	args[len(args)-1] = "--json"
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != 0 || strings.Contains(stderr.String(), "private-data") {
		t.Fatalf("malformed record code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	partialArgs := slices.Concat(args, []string{"--source", "pi=" + filepath.Join(t.TempDir(), "missing")})
	code := executeCLI(t.Context(), partialArgs, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !json.Valid(stdout.Bytes()) || strings.Contains(stderr.String(), "private-data") {
		t.Fatalf("partial output code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "search incomplete; some histories could not be searched") || strings.Contains(stderr.String(), "history search incomplete") {
		t.Fatalf("partial diagnostic = %q", stderr.String())
	}
}

func appendMalformedRecord(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not-json private-data\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchCLIRejectsInvalidOptionsBeforeConfig(t *testing.T) {
	for _, args := range [][]string{
		{"search", ""},
		{"search", "x", "--limit", "-1"},
		{"search", "x", "--agent", "bad"},
		{"search", "x", "--source", "pi"},
		{"search", "x", "--source", "pi="},
		{"search", "x", "--agent", "codex", "--source", "pi=/tmp/history.jsonl"},
		{"search", "(", "--regex"},
		{"search", "--since", "yesterday"},
		{"search", "--since", "1d", "--until", "2d"},
		{"search", "x", "--stream", "--limit", "1"},
		{"search", "--format", "title"},
		{"search", "--group-by", "week"},
		{"search", "--presence", "unknown"},
		{"search", "--sort", "matches"},
		{"search", "x", "--not", " "},
	} {
		configPath := filepath.Join(t.TempDir(), "missing-config.toml")
		args = append([]string{"--config", configPath}, args...)
		var stdout, stderr bytes.Buffer
		if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != exitCodeUsage || stdout.Len() != 0 || strings.Contains(stderr.String(), "read config") {
			t.Fatalf("validation code=%d stderr=%q", code, stderr.String())
		}
	}
}

func TestSearchCLIOptionalLimit(t *testing.T) {
	path := searchCLIFixture(t)
	for _, limit := range []string{"", "0", "1001"} {
		args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "--json", "search", "refresh", "--source", "pi=" + path}
		if limit != "" {
			args = append(args, "--limit", limit)
		}
		var stdout, stderr bytes.Buffer
		if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		var result history.Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Matches) != 1 || result.Truncated {
			t.Fatalf("limit %q: matches=%d truncated=%t", limit, len(result.Matches), result.Truncated)
		}
	}
}

func TestSearchJSONEscapesTerminalControlsWithoutChangingContent(t *testing.T) {
	path := searchCLIFixture(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "--json", "search", "refresh", "--source", "pi=" + path}
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(stdout.String(), "\x1b\u202e") {
		t.Fatalf("raw terminal controls in JSON: %q", stdout.String())
	}
	var result history.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || !strings.Contains(result.Matches[0].Excerpts[0].Text, "\x1b[31m\u202e") {
		t.Fatal("JSON escaping changed transcript content")
	}
}

func TestSearchCLIEmptyStateTracksSearchCompletion(t *testing.T) {
	path := searchCLIFixture(t)
	store := filepath.Join(t.TempDir(), "state.json")
	var stdout, stderr bytes.Buffer

	complete := []string{"--no-config", "--store", store, "search", "absent-needle", "--source", "pi=" + path}
	if code := executeCLI(t.Context(), complete, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("complete search code=%d stderr=%q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "No matching conversations." {
		t.Fatalf("complete empty state = %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	incomplete := []string{"--no-config", "--store", store, "search", "absent-needle", "--source", "pi=" + filepath.Join(t.TempDir(), "missing")}
	if code := executeCLI(t.Context(), incomplete, strings.NewReader(""), &stdout, &stderr); code != exitCodeGeneral {
		t.Fatalf("incomplete search code=%d stderr=%q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "No matching conversations; some sources could not be searched." {
		t.Fatalf("incomplete empty state = %q", got)
	}
	if !strings.Contains(stderr.String(), "search incomplete; some histories could not be searched") || strings.Contains(stderr.String(), "history search incomplete") {
		t.Fatalf("incomplete diagnostic = %q", stderr.String())
	}
}

func TestSearchCLICancellationExitsInterrupted(t *testing.T) {
	path := searchCLIFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "search", "refresh", "--source", "pi=" + path}
	var stdout, stderr bytes.Buffer
	if code := executeCLI(ctx, args, strings.NewReader(""), &stdout, &stderr); code != exitCodeInterrupted {
		t.Fatalf("canceled search code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "search incomplete") {
		t.Fatalf("cancellation reported an incomplete search: %q", stderr.String())
	}
}

func TestSearchCLIUsageErrorsShowCorrectedUsage(t *testing.T) {
	path := searchCLIFixture(t)
	for _, test := range []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "source without path",
			args:     []string{"search", "refresh", "--source", "pi"},
			expected: []string{`invalid --source "pi": expected agent=path`, "for example --source codex=/path/to/sessions"},
		},
		{
			name:     "empty source path",
			args:     []string{"search", "refresh", "--source", "pi="},
			expected: []string{`invalid --source "pi=": expected agent=path`},
		},
		{
			name:     "conflicting harness selection",
			args:     []string{"search", "refresh", "--agent", "codex", "--source", "pi=" + path},
			expected: []string{"conflicting --harness and --source harnesses", `--source "pi=`, "aht search --help"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--no-config"}, test.args...)
			if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != exitCodeUsage {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, exitCodeUsage, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("usage error wrote stdout: %q", stdout.String())
			}
			for _, fragment := range test.expected {
				if !strings.Contains(stderr.String(), fragment) {
					t.Fatalf("stderr = %q, want %q", stderr.String(), fragment)
				}
			}
		})
	}
}

func TestSearchCLIAgentAndSourceAgree(t *testing.T) {
	path := searchCLIFixture(t)
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "--json", "search", "refresh", "--agent", "pi", "--source", "pi=" + path}
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("matching selection failed: %v stderr=%q", err, stderr.String())
	}
	var result history.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matching selection matches = %#v", result.Matches)
	}
}

func TestSearchCLIExplicitSourceOverridesIgnoredHarness(t *testing.T) {
	home := searchCLIHome(t)
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	historyPath := filepath.Join(sessions, "history.jsonl")
	writeSearchFixture(t, historyPath)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[filter]\nignore_harnesses = [\"pi\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--config", configPath, "--store", filepath.Join(t.TempDir(), "state.json"), "--json", "search", "refresh", "--dir", "/work"}

	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), base, &stdout, &stderr); err != nil {
		t.Fatalf("ignored search failed: %v stderr=%q", err, stderr.String())
	}
	var ignored history.Result
	if err := json.Unmarshal(stdout.Bytes(), &ignored); err != nil {
		t.Fatal(err)
	}
	if len(ignored.Matches) != 0 || searchSourceStatus(t, ignored, registry.Harness("pi"), sessions) != "skipped" {
		t.Fatalf("configured ignore list lost effect: %#v", ignored)
	}

	stdout.Reset()
	stderr.Reset()
	explicit := append(append([]string{}, base...), "--source", "pi="+historyPath)
	if err := runTestCLI(t.Context(), explicit, &stdout, &stderr); err != nil {
		t.Fatalf("explicit source search failed: %v stderr=%q", err, stderr.String())
	}
	var selected history.Result
	if err := json.Unmarshal(stdout.Bytes(), &selected); err != nil {
		t.Fatal(err)
	}
	if len(selected.Matches) != 1 || searchSourceStatus(t, selected, registry.Harness("pi"), historyPath) != "searched" {
		t.Fatalf("explicit source did not override the ignore list: %#v", selected)
	}
}

func TestSearchCLIUnsupportedSourceSummaryListsHarnessOnce(t *testing.T) {
	searchCLIHome(t)
	first := filepath.Join(t.TempDir(), "first.jsonl")
	second := filepath.Join(t.TempDir(), "second.jsonl")
	writeSearchFixture(t, first)
	writeSearchFixture(t, second)
	args := []string{
		"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"),
		"search", "refresh",
		"--source", "cursor=" + first,
		"--source", "cursor=" + second,
	}
	var stdout, stderr bytes.Buffer
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != exitCodeGeneral {
		t.Fatalf("unsupported sources code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "History readers unavailable: cursor.") {
		t.Fatalf("missing harness summary: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "cursor, cursor") {
		t.Fatalf("harness listed twice: %q", stderr.String())
	}
}

func TestSearchMatchTTY(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	match := history.Match{
		Conversation: history.Conversation{
			Harness:   registry.Harness("pi"),
			SessionID: "01a0c324-11ca-7000-894a-0e31b911d7ae",
			Title:     "Fix failing workflow",
			CWD:       "/work/project",
			Path:      "/work/project/session.jsonl",
			UpdatedAt: now,
		},
		RegistryStates: []history.RegistryState{
			{Presence: registry.PresenceLive},
		},
		Excerpts: []history.Excerpt{
			{Role: "user", Text: "Please fix the failing workflow"},
			{Role: "assistant", Text: "I fixed the workflow test"},
		},
	}
	match.ResumeCommand = []string{"pi", "--session", "/work/my project/session.jsonl"}
	match.Excerpts[0].Matches = []history.Span{{Start: 23, End: 31}}
	if err := app.writeSearchMatchTTY(match, false); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Fix failing workflow") {
		t.Errorf("TTY output missing title: %q", output)
	}
	if !strings.Contains(output, "01a0c324-11ca-7000-894a-0e31b911d7ae") {
		t.Errorf("TTY output missing the full session ID: %q", output)
	}
	if !strings.Contains(output, "cd /work/project && pi --session '/work/my project/session.jsonl'") {
		t.Errorf("TTY output missing a pasteable resume command: %q", output)
	}
	if !strings.Contains(output, highlightStart+"workflow"+styleReset) {
		t.Errorf("TTY output did not highlight the match span: %q", output)
	}
	for _, text := range []string{"live", "Please fix the failing", "I fixed the"} {
		if !strings.Contains(output, text) {
			t.Errorf("TTY output missing %q: %q", text, output)
		}
	}
}

func TestSearchCLIRoleFilter(t *testing.T) {
	path := searchCLIFixture(t)
	store := filepath.Join(t.TempDir(), "state.json")
	for _, test := range []struct {
		role    string
		matches int
		valid   bool
	}{
		{"user", 1, true},
		{"agent", 0, true},
		{"assistant", 0, true},
		{"all", 1, true},
		{"tool", 0, true},
		{"invalid", 0, false},
	} {
		t.Run("role="+test.role, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"--no-config", "--store", store, "--json", "search", "Refresh token", "--source", "pi=" + path, "--role", test.role}
			code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
			if !test.valid {
				if code != exitCodeUsage {
					t.Fatalf("expected usage error for invalid role %q, got code %d", test.role, code)
				}
				return
			}
			if code != 0 {
				t.Fatalf("expected success for role %q, got code %d, stderr: %s", test.role, code, stderr.String())
			}
			var result history.Result
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Matches) != test.matches {
				t.Fatalf("role %q: got %d matches, want %d", test.role, len(result.Matches), test.matches)
			}
		})
	}
}

// conversationFixture writes pi histories for the sessions, each with one user
// message mentioning its topic.
func conversationFixture(t *testing.T, sessions map[string]string) string {
	t.Helper()
	searchCLIHome(t)
	root := t.TempDir()
	for id, cwd := range sessions {
		body := `{"type":"session","id":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-09-01T10:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"user","content":"topic ` + id + ` refresh"}}
`
		writeSearchFixtureBody(t, filepath.Join(root, id+".jsonl"), body)
	}
	return root
}

func writeSearchFixtureBody(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSearchOutput(t *testing.T, args ...string) string {
	t.Helper()
	args = append([]string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json")}, args...)
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("%v: %v stderr=%q", args, err, stderr.String())
	}
	return stdout.String()
}

func TestSearchCLIListsWithoutText(t *testing.T) {
	root := conversationFixture(t, map[string]string{"alpha": "/work/a", "beta": "/work/b"})
	output := runSearchOutput(t, "search", "--source", "pi="+root, "--dir", "/work/a")
	if !strings.Contains(output, "alpha") || strings.Contains(output, "beta") || !strings.Contains(output, "topic alpha refresh") {
		t.Fatalf("listing = %q", output)
	}
	var result history.Result
	if err := json.Unmarshal([]byte(runSearchOutput(t, "--json", "search", "--source", "pi="+root)), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 2 || len(result.Matches[0].Excerpts) != 0 {
		t.Fatalf("JSON listing = %#v", result.Matches)
	}
}

func TestSearchCLIFormatsAndGroups(t *testing.T) {
	root := conversationFixture(t, map[string]string{"alpha": "/work/a", "beta": "/work/a", "gamma": "/work/c"})
	ids := strings.Fields(runSearchOutput(t, "search", "refresh", "--source", "pi="+root, "--format", "id"))
	if strings.Join(ids, ",") != "alpha,beta,gamma" {
		t.Fatalf("ids = %q", ids)
	}
	resume := runSearchOutput(t, "search", "gamma", "--source", "pi="+root, "--format", "resume")
	if want := "cd /work/c && pi --session " + filepath.Join(root, "gamma.jsonl") + "\n"; resume != want {
		t.Fatalf("resume = %q, want %q", resume, want)
	}
	var groups []searchGroup
	if err := json.Unmarshal([]byte(runSearchOutput(t, "--json", "search", "--source", "pi="+root, "--group-by", "project")), &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Group != "/work/a" || groups[0].Conversations != 2 || groups[1].Conversations != 1 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestSearchCLIStreamsJSONLines(t *testing.T) {
	root := conversationFixture(t, map[string]string{"alpha": "/work/a", "beta": "/work/b"})
	output := runSearchOutput(t, "--json", "search", "refresh", "--source", "pi="+root, "--stream")
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("stream lines = %q", output)
	}
	for _, line := range lines {
		var match history.Match
		if err := json.Unmarshal([]byte(line), &match); err != nil || match.Conversation.SessionID == "" {
			t.Fatalf("stream line %q: %v", line, err)
		}
	}
}

func TestHighlightedLinesWrapAndMarkSpans(t *testing.T) {
	t.Parallel()
	//nolint:gosmopolitan // Wide and multi-byte runes exercise display-width wrapping.
	value := "İİ alpha\x1b  beta 你好 gamma"
	start := strings.Index(value, "beta")
	lines := highlightedLines(value, []history.Span{{Start: start, End: start + len("beta")}}, 10)
	joined := strings.Join(lines, "|")
	//nolint:gosmopolitan // Wide runes exercise display-width wrapping.
	want := "İİ alpha|" + highlightStart + "beta" + styleReset + " 你好|gamma"
	if joined != want {
		t.Fatalf("lines = %q, want %q", joined, want)
	}
	for _, line := range lines {
		if !utf8.ValidString(line) {
			t.Fatalf("invalid UTF-8 line %q", line)
		}
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"/work/app":       "/work/app",
		"--resume":        "--resume",
		"my project":      "'my project'",
		"it's":            `'it'\''s'`,
		"":                "''",
		"$(touch x)":      "'$(touch x)'",
		"/work/ünïcode-1": "/work/ünïcode-1",
	} {
		if got := shellQuote(value); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestParseSearchTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Time{
		"":                     {},
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"90m":                  now.Add(-90 * time.Minute),
		"2026-09-01T08:00:00Z": time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
		"2026-09-01":           time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), //nolint:gosmopolitan // Bare dates are parsed in the user's local zone.
	} {
		got, err := parseSearchTime(value, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseSearchTime(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"yesterday", "-1d", "0d", "7"} {
		if _, err := parseSearchTime(value, now); err == nil {
			t.Errorf("parseSearchTime(%q) accepted", value)
		}
	}
}

func piSession(id, cwd, at string) string {
	return `{"type":"session","id":"` + id + `","cwd":"` + cwd + `","timestamp":"` + at + `"}
{"type":"message","id":"u1","timestamp":"` + at + `","message":{"role":"user","content":"topic ` + id + ` refresh"}}
`
}

func TestSearchCLIGroupsByDayNewestFirst(t *testing.T) {
	searchCLIHome(t)
	root := t.TempDir()
	writeSearchFixtureBody(t, filepath.Join(root, "early.jsonl"), piSession("early", "/work/a", "2026-08-01T12:00:00Z"))
	writeSearchFixtureBody(t, filepath.Join(root, "late-1.jsonl"), piSession("late-1", "/work/a", "2026-09-01T12:00:00Z"))
	writeSearchFixtureBody(t, filepath.Join(root, "late-2.jsonl"), piSession("late-2", "/work/b", "2026-09-01T12:30:00Z"))
	writeSearchFixtureBody(t, filepath.Join(root, "undated.jsonl"), `{"type":"session","id":"undated","cwd":"/work/c"}
{"type":"message","id":"u1","message":{"role":"user","content":"topic undated refresh"}}
`)
	var groups []searchGroup
	if err := json.Unmarshal([]byte(runSearchOutput(t, "--json", "search", "refresh", "--source", "pi="+root, "--group-by", "day")), &groups); err != nil {
		t.Fatal(err)
	}
	day := func(at time.Time) string { return at.In(time.Local).Format(time.DateOnly) } //nolint:gosmopolitan // matches --group-by day local buckets
	want := []searchGroup{
		{Group: day(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)), Conversations: 2, Messages: 2},
		{Group: day(time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)), Conversations: 1, Messages: 1},
		{Group: "-", Conversations: 1, Messages: 1},
	}
	if diff := cmp.Diff(want, groups); diff != "" {
		t.Fatalf("day groups (-want +got):\n%s", diff)
	}
	table := runSearchOutput(t, "search", "--source", "pi="+root, "--group-by", "harness")
	if !strings.Contains(table, "Harness") || !regexp.MustCompile(`(?m)^pi\s+4\s+4\s*$`).MatchString(table) {
		t.Fatalf("harness table = %q", table)
	}
}

func TestSearchCLIStreamsTextMatches(t *testing.T) {
	root := conversationFixture(t, map[string]string{"alpha": "/work/a", "beta": "/work/b"})
	output := runSearchOutput(t, "search", "refresh", "--source", "pi="+root, "--stream")
	for _, id := range []string{"alpha", "beta"} {
		if !strings.Contains(output, "pi "+id) || !strings.Contains(output, "topic "+id+" refresh") || !strings.Contains(output, "pi --session "+filepath.Join(root, id+".jsonl")) {
			t.Fatalf("stream output = %q, missing %s", output, id)
		}
	}
}

func TestSearchCLIMalformedRecordsWarnWithoutFailing(t *testing.T) {
	searchCLIHome(t)
	root := t.TempDir()
	body := piSession("damaged", "/work/a", "2026-09-01T12:00:00Z") + "{broken\n{broken\n{broken\n{broken\n"
	writeSearchFixtureBody(t, filepath.Join(root, "damaged.jsonl"), body)
	var stdout, stderr bytes.Buffer
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "search", "refresh", "--source", "pi=" + root}
	if code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "damaged") || !strings.Contains(stderr.String(), "skipped 4 malformed history records") || strings.Contains(stderr.String(), "could not be read") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestSearchProgressStaysOffNonTerminalStderr(t *testing.T) {
	root := conversationFixture(t, map[string]string{"alpha": "/work/a"})
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderr.Close() }()
	var stdout bytes.Buffer
	args := []string{"--no-config", "--store", filepath.Join(t.TempDir(), "state.json"), "search", "refresh", "--source", "pi=" + root}
	if err := runTestCLI(t.Context(), args, &stdout, stderr); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsAny(written, "\r\x1b") {
		t.Fatalf("stderr = %q", written)
	}
}

func TestSearchProgressClearsItsLine(t *testing.T) {
	var stderr bytes.Buffer
	progress := &searchProgress{app: &application{stderr: &stderr}, enabled: true}
	progress.clear()
	if stderr.Len() != 0 {
		t.Fatalf("clear without a shown line wrote %q", stderr.String())
	}
	progress.update(history.Progress{Source: history.Source{Harness: registry.Harness("pi")}, Done: 1, Total: 2, Refreshed: 1})
	if !strings.Contains(stderr.String(), "Scanning pi history 1/2, indexed 1 changed") {
		t.Fatalf("progress = %q", stderr.String())
	}
	progress.clear()
	if !strings.HasSuffix(stderr.String(), "\r\x1b[2K") {
		t.Fatalf("progress line was not erased: %q", stderr.String())
	}
	shown := stderr.Len()
	progress.clear()
	if stderr.Len() != shown {
		t.Fatalf("second clear wrote %q", stderr.String()[shown:])
	}
}
