package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
