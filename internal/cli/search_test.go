package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/history"
	"github.com/zigai/aht/pkg/registry"
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
	stdout.Reset()
	stderr.Reset()
	args[len(args)-1] = "--json"
	code := executeCLI(t.Context(), args, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !json.Valid(stdout.Bytes()) || strings.Contains(stderr.String(), "private-data") {
		t.Fatalf("partial output code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "search incomplete; some histories could not be searched") || strings.Contains(stderr.String(), "history search incomplete") {
		t.Fatalf("partial diagnostic = %q", stderr.String())
	}
}

func TestSearchCLIRejectsInvalidOptionsBeforeConfig(t *testing.T) {
	for _, args := range [][]string{
		{"search"},
		{"search", ""},
		{"search", "x", "--limit", "-1"},
		{"search", "x", "--agent", "bad"},
		{"search", "x", "--source", "pi"},
		{"search", "x", "--source", "pi="},
		{"search", "x", "extra"},
		{"search", "x", "--agent", "codex", "--source", "pi=/tmp/history.jsonl"},
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
			name:     "missing argument",
			args:     []string{"search"},
			expected: []string{"expected exactly one text argument", "aht search --help"},
		},
		{
			name:     "extra argument",
			args:     []string{"search", "refresh", "token"},
			expected: []string{"expected exactly one text argument, received 2", `aht search "refresh token"`},
		},
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
			expected: []string{"conflicting --agent and --source harnesses", `--agent "codex"`, `--source "pi=`, "aht search --help"},
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
	if len(ignored.Matches) != 0 || searchSourceStatus(t, ignored, registry.HarnessPi, sessions) != "skipped" {
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
	if len(selected.Matches) != 1 || searchSourceStatus(t, selected, registry.HarnessPi, historyPath) != "searched" {
		t.Fatalf("explicit source did not override the ignore list: %#v", selected)
	}
}

func TestSearchCLIHelpStatesResultOrdering(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := executeCLI(t.Context(), []string{"search", "--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
	for _, fragment := range []string{"most recently updated conversation first", "--limit keeps the newest <count>", "newest first (0 means unlimited)"} {
		if !strings.Contains(stdout.String(), fragment) {
			t.Fatalf("help missing %q:\n%s", fragment, stdout.String())
		}
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

func TestHighlightNeedle(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		text          string
		needle        string
		caseSensitive bool
		want          string
	}{
		{
			name:          "case-insensitive simple",
			text:          "Hello World",
			needle:        "world",
			caseSensitive: false,
			want:          "Hello \x1b[1;36mWorld\x1b[0m",
		},
		{
			name:          "case-insensitive multiple",
			text:          "test one, TEST two, Test three",
			needle:        "test",
			caseSensitive: false,
			want:          "\x1b[1;36mtest\x1b[0m one, \x1b[1;36mTEST\x1b[0m two, \x1b[1;36mTest\x1b[0m three",
		},
		{
			name:          "case-sensitive match",
			text:          "Exact Match and exact mismatch",
			needle:        "Exact",
			caseSensitive: true,
			want:          "\x1b[1;36mExact\x1b[0m Match and exact mismatch",
		},
		{
			name:          "no match",
			text:          "completely unrelated content",
			needle:        "absent",
			caseSensitive: false,
			want:          "completely unrelated content",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := highlightNeedle(tt.text, tt.needle, tt.caseSensitive)
			if got != tt.want {
				t.Errorf("highlightNeedle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchMatchTTY(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	match := history.Match{
		Conversation: history.Conversation{
			Harness:   registry.HarnessPi,
			SessionID: "01a0c324-11ca-7000-894a-0e31b911d7ae",
			Title:     "Fix failing workflow",
			CWD:       "/work/project",
			Path:      "/work/project/session.jsonl",
			UpdatedAt: now,
		},
		Live: []history.LiveState{
			{Presence: registry.PresenceLive},
		},
		Excerpts: []history.Excerpt{
			{Role: "user", Text: "Please fix the failing workflow"},
			{Role: "assistant", Text: "I fixed the workflow test"},
		},
	}
	if err := app.writeSearchMatchTTY(match, history.Query{Text: "workflow"}, false); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Fix failing workflow") {
		t.Errorf("TTY output missing title: %q", output)
	}
	if !strings.Contains(output, "01a0c324") {
		t.Errorf("TTY output missing inline short ID: %q", output)
	}
	if !strings.Contains(output, "● live") {
		t.Errorf("TTY output missing live badge: %q", output)
	}
	if !strings.Contains(output, "├─") || !strings.Contains(output, "└─") {
		t.Errorf("TTY output missing tree connectors: %q", output)
	}
	if strings.Contains(output, "\x1b[2m│\x1b[0m\n") {
		t.Errorf("TTY output must not contain standalone stem line: %q", output)
	}
	if !strings.Contains(output, "\x1b[1;36mworkflow\x1b[0m") {
		t.Errorf("TTY output missing highlighted needle: %q", output)
	}
	if !strings.Contains(output, "\x1b[1;33muser:\x1b[0m") || !strings.Contains(output, "\x1b[1;37massistant:\x1b[0m") {
		t.Errorf("TTY output missing distinct role colors: %q", output)
	}
}

func TestCleanSessionTitle(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		input string
		want  string
	}{
		{
			input: "# AGENTS.md instructions for /home/zigai/Projects/loti <INSTRUCTIONS> When calling spawn_agent, never set the...",
			want:  "When calling spawn_agent, never set the...",
		},
		{
			input: "<environment_context> <cwd>/home/user/app</cwd> </environment_context> Real task description",
			want:  "Real task description",
		},
		{
			input: "Normal session title",
			want:  "Normal session title",
		},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got := cleanSessionTitle(tt.input)
			if got != tt.want {
				t.Errorf("cleanSessionTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveSearchTitle(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		conv     history.Conversation
		excerpts []history.Excerpt
		want     string
	}{
		{
			name: "fallback when title is a truncated fragment",
			conv: history.Conversation{
				SessionID: "sess1",
				Title:     "When calli…",
			},
			excerpts: []history.Excerpt{
				{Role: "user", Text: "No not this. It was not for freelance jobs, it was for regular jobs."},
			},
			want: "No not this. It was not for freelance jobs, it was for regular jobs.",
		},
		{
			name: "fallback when title contains environment context",
			conv: history.Conversation{
				SessionID: "sess2",
				Title:     "<environment_context> <cwd>/home/user/project</cwd> <approval_policy>on…",
			},
			excerpts: []history.Excerpt{
				{Role: "user", Text: "Professional settings, including an internship at nChain and freelance projects."},
			},
			want: "Professional settings, including an internship at nChain and freelance projects.",
		},
		{
			name: "retains clean existing title",
			conv: history.Conversation{
				SessionID: "sess3",
				Title:     "Fix failing GitHub Actions workflow",
			},
			excerpts: []history.Excerpt{
				{Role: "user", Text: "Please fix GHA"},
			},
			want: "Fix failing GitHub Actions workflow",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSearchTitle(tt.conv, tt.excerpts)
			if got != tt.want {
				t.Errorf("resolveSearchTitle() = %q, want %q", got, tt.want)
			}
		})
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
