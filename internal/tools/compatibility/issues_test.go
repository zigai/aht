package main

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zigai/aht/v2/internal/tools/githubapi"
)

var grokRegression = issueStatus{Harness: "grok", Version: "1.0.41", Successful: "1.0.39", RunURL: "https://example.invalid/run/1"}

type issueCall struct {
	method, path string
	body         map[string]string
}

// fakeIssues serves the open-issue listing and records every mutation.
type fakeIssues struct {
	mu    sync.Mutex
	open  []map[string]any
	calls []issueCall
}

func (f *fakeIssues) client(t *testing.T) *githubapi.Client {
	t.Helper()
	api := githubapi.New(f.serve(t), "fixture-token")
	api.RetryDelay = 0
	return api
}

// serve starts the fake and returns its API root.
func (f *fakeIssues) serve(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodGet {
			query := r.URL.Query()
			if r.URL.Path != "/repos/fixture/aht/issues" || query.Get("state") != "open" || query.Get("creator") != "github-actions[bot]" {
				t.Errorf("listing = %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode(f.open)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		f.calls = append(f.calls, issueCall{method: r.Method, path: r.URL.Path, body: body})
		_, _ = io.WriteString(w, `{"number":99}`)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func openIssue(number int, login string, item issueStatus) map[string]any {
	return map[string]any{"number": number, "title": issueTitle(item), "body": issueBody(item), "user": map[string]string{"login": login}}
}

func syncFixture(t *testing.T, fake *fakeIssues, report issueReport) {
	t.Helper()
	if err := syncIssues(t.Context(), fake.client(t), "fixture/aht", report, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestIssueSyncOpensOneIssueForUntrackedRegression(t *testing.T) {
	fake := &fakeIssues{}
	syncFixture(t, fake, issueReport{Open: []issueStatus{grokRegression}})
	if len(fake.calls) != 1 || fake.calls[0].method != http.MethodPost || fake.calls[0].path != "/repos/fixture/aht/issues" {
		t.Fatalf("calls = %+v", fake.calls)
	}
	created := fake.calls[0].body
	if created["title"] != "Compatibility regression: grok" {
		t.Fatalf("title = %q", created["title"])
	}
	for _, want := range []string{`\*\*grok 1\.0\.41\*\*`, `Last successful release: 1\.0\.39`, `(?m)^<!-- aht-compatibility:grok -->$`} {
		if !regexp.MustCompile(want).MatchString(created["body"]) {
			t.Errorf("body lacks %s:\n%s", want, created["body"])
		}
	}
}

func TestIssueSyncLeavesUnchangedIssueAndUpdatesStaleOne(t *testing.T) {
	unchanged := &fakeIssues{open: []map[string]any{openIssue(5, issueCreator, grokRegression)}}
	syncFixture(t, unchanged, issueReport{Open: []issueStatus{grokRegression}})
	if len(unchanged.calls) != 0 {
		t.Fatalf("unchanged issue edited: %+v", unchanged.calls)
	}

	older := grokRegression
	older.Version = "1.0.40"
	stale := &fakeIssues{open: []map[string]any{openIssue(5, issueCreator, older)}}
	syncFixture(t, stale, issueReport{Open: []issueStatus{grokRegression}})
	if len(stale.calls) != 1 || stale.calls[0].method != http.MethodPatch || stale.calls[0].path != "/repos/fixture/aht/issues/5" {
		t.Fatalf("calls = %+v", stale.calls)
	}
	if !strings.Contains(stale.calls[0].body["body"], "grok 1.0.41") {
		t.Fatalf("updated body = %q", stale.calls[0].body["body"])
	}
}

func TestIssueSyncCommentsAndClosesResolvedIssue(t *testing.T) {
	tests := []struct {
		name    string
		item    issueStatus
		comment string
	}{
		{"passed", issueStatus{Harness: "grok", Version: "1.0.42", RunURL: "https://example.invalid/run/2", Reason: "passed"}, "grok 1.0.42 passed in https://example.invalid/run/2."},
		{"above maximum", issueStatus{Harness: "grok", Version: "1.0.42", MaxVersion: "1.0.41", Reason: reasonAboveMaximum}, "grok 1.0.42 is above the supported maximum 1.0.41, so the adapter's `MaxVersion` now tracks this failure."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeIssues{open: []map[string]any{openIssue(5, issueCreator, grokRegression)}}
			syncFixture(t, fake, issueReport{Resolved: []issueStatus{tt.item}})
			want := []issueCall{
				{method: http.MethodPost, path: "/repos/fixture/aht/issues/5/comments", body: map[string]string{"body": tt.comment}},
				{method: http.MethodPatch, path: "/repos/fixture/aht/issues/5", body: map[string]string{"state": "closed", "state_reason": "completed"}},
			}
			if !equalCalls(fake.calls, want) {
				t.Fatalf("calls = %+v, want %+v", fake.calls, want)
			}
		})
	}
}

func TestIssueSyncIgnoresIssuesAndPullRequestsItDoesNotOwn(t *testing.T) {
	pullRequest := openIssue(2, issueCreator, grokRegression)
	pullRequest["pull_request"] = map[string]any{}
	fake := &fakeIssues{open: []map[string]any{
		openIssue(1, "someone", grokRegression),
		pullRequest,
		{"number": 3, "title": "Unrelated", "body": "mentions grok", "user": map[string]string{"login": issueCreator}},
	}}
	resolved := grokRegression
	resolved.Reason = "passed"
	syncFixture(t, fake, issueReport{Resolved: []issueStatus{resolved}})
	if len(fake.calls) != 0 {
		t.Fatalf("edited an issue it does not own: %+v", fake.calls)
	}
}

func TestIssuesCommandReadsFinishReport(t *testing.T) {
	fake := &fakeIssues{}
	directory := t.TempDir()
	data, err := json.Marshal(issueReport{Open: []issueStatus{grokRegression}, Resolved: []issueStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "issues.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AHT_COMPAT_WORK": directory, "GITHUB_REPOSITORY": "fixture/aht", "GITHUB_API_URL": fake.serve(t)}
	app := application{getenv: func(key string) string { return env[key] }, stdout: io.Discard, stderr: io.Discard}
	if err := app.run(t.Context(), []string{"issues"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || fake.calls[0].method != http.MethodPost {
		t.Fatalf("calls = %+v", fake.calls)
	}
}

func equalCalls(got, want []issueCall) bool {
	return slices.EqualFunc(got, want, func(a, b issueCall) bool {
		return a.method == b.method && a.path == b.path && maps.Equal(a.body, b.body)
	})
}
