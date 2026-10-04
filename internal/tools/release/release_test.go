package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/tools/githubapi"
)

var (
	fixtureSHA = strings.Repeat("a", 40)
	fixtureEnv = map[string]string{
		"GITHUB_REPOSITORY": "fixture/aht",
		"GITHUB_REF":        "refs/tags/v1.2.3",
		"GITHUB_RUN_ID":     "42",
		"AHT_RELEASE_SHA":   fixtureSHA,
	}
	successfulRun = workflowRun{
		ID: 10, HeadSHA: fixtureSHA, Event: "push", HeadBranch: "master", Path: ".github/workflows/ci.yml",
		Status: "completed", Conclusion: "success", HTMLURL: "https://example.invalid/run/10",
	}
)

func successfulJobs() []workflowJob {
	jobs := make([]workflowJob, 0, len(requiredChecks))
	for _, name := range requiredChecks {
		jobs = append(jobs, workflowJob{Name: name, Conclusion: "success"})
	}
	return jobs
}

// fakeGitHub serves the release, workflow, and issue endpoints for fixture/aht.
type fakeGitHub struct {
	mu           sync.Mutex
	release      *release
	deleted      []int64
	runs         []workflowRun
	jobs         []workflowJob
	issues       []regressionIssue
	runSnapshots [][]workflowRun
	// fail, keyed by "METHOD /path", returns a status after optionally applying
	// the request, simulating a rejected call or a lost response.
	fail map[string]failure
}

type failure struct {
	status int
	apply  bool
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	f.registerGateHandlers(t, mux)
	mux.HandleFunc("GET /repos/fixture/aht/releases/tags/v1.2.3", func(w http.ResponseWriter, _ *http.Request) {
		if f.release == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, f.release)
	})
	mux.HandleFunc("POST /repos/fixture/aht/releases", func(w http.ResponseWriter, r *http.Request) {
		var create newRelease
		if err := json.NewDecoder(r.Body).Decode(&create); err != nil || !create.Draft {
			t.Errorf("create = %+v, %v", create, err)
		}
		f.release = &release{ID: 7, TagName: create.TagName, TargetCommitish: create.TargetCommitish, Draft: create.Draft, Body: create.Body}
		writeJSON(w, f.release)
	})
	mux.HandleFunc("GET /repos/fixture/aht/releases/7", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, f.release)
	})
	mux.HandleFunc("PATCH /repos/fixture/aht/releases/7", func(w http.ResponseWriter, r *http.Request) {
		var update releaseDraft
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			t.Error(err)
		}
		f.release.Draft = update.Draft
		writeJSON(w, f.release)
	})
	mux.HandleFunc("DELETE /repos/fixture/aht/releases/7", func(w http.ResponseWriter, _ *http.Request) {
		f.deleted = append(f.deleted, f.release.ID)
		f.release = nil
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if fail, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
			if fail.apply {
				mux.ServeHTTP(httptest.NewRecorder(), r)
			}
			http.Error(w, `{"message":"failed"}`, fail.status)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (f *fakeGitHub) registerGateHandlers(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	mux.HandleFunc("GET /repos/fixture/aht/actions/workflows/ci.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("head_sha") != fixtureSHA || query.Get("event") != "push" || query.Get("branch") != releaseBranch {
			t.Errorf("runs query = %s", r.URL.RawQuery)
		}
		if len(f.runSnapshots) != 0 {
			f.runs = f.runSnapshots[0]
			f.runSnapshots = f.runSnapshots[1:]
		}
		writeJSON(w, map[string]any{"workflow_runs": f.runs})
	})
	mux.HandleFunc("GET /repos/fixture/aht/actions/runs/{run}/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") != "latest" {
			t.Errorf("jobs query = %s", r.URL.RawQuery)
		}
		writeJSON(w, map[string]any{"jobs": f.jobs})
	})
	mux.HandleFunc("GET /repos/fixture/aht/issues", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != "open" || query.Get("creator") != issueCreator || query.Get("per_page") != "100" {
			t.Errorf("issues query = %s", r.URL.RawQuery)
		}
		writeJSON(w, f.issues)
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type fixture struct {
	github  *fakeGitHub
	app     application
	outputs string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	github := &fakeGitHub{runs: []workflowRun{successfulRun}, jobs: successfulJobs(), fail: map[string]failure{}}
	server := httptest.NewServer(github.handler(t))
	t.Cleanup(server.Close)
	api := githubapi.New(server.URL, "fixture-token")
	api.RetryDelay = 0
	outputs := filepath.Join(t.TempDir(), "outputs")
	env := map[string]string{"GITHUB_OUTPUT": outputs, "AHT_RELEASE_NOTES": writeNotes(t)}
	maps.Copy(env, fixtureEnv)
	getenv := func(key string) string { return env[key] }
	return &fixture{github: github, app: application{api: api, getenv: getenv, stdout: io.Discard}, outputs: outputs}
}

func writeNotes(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("## Features\n\nA change\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *fixture) run(t *testing.T, command string) error {
	t.Helper()
	return f.app.run(t.Context(), []string{command})
}

// output returns the last value written for name.
func (f *fixture) output(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(f.outputs)
	if err != nil {
		return ""
	}
	value := ""
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(line, name+"="); ok {
			value = strings.TrimSpace(rest)
		}
	}
	return value
}

func (f *fixture) setEnv(key, value string) {
	getenv := f.app.getenv
	f.app.getenv = func(name string) string {
		if name == key {
			return value
		}
		return getenv(name)
	}
}

func TestReleaseRequiresNewestExactCommitCIAndEveryGate(t *testing.T) {
	withoutGate := successfulJobs()[:3]
	skippedGate := successfulJobs()
	skippedGate[3].Conclusion = "skipped"
	duplicateGate := append(successfulJobs(), workflowJob{Name: "verify-linux", Conclusion: "success"})
	with := func(change func(*workflowRun)) workflowRun {
		run := successfulRun
		change(&run)
		return run
	}
	tests := []struct {
		name string
		runs []workflowRun
		jobs []workflowJob
		pass bool
	}{
		{"successful exact revision", []workflowRun{successfulRun}, successfulJobs(), true},
		{"missing run", nil, successfulJobs(), false},
		{"other revision", []workflowRun{with(func(r *workflowRun) { r.HeadSHA = strings.Repeat("b", 40) })}, successfulJobs(), false},
		{"other workflow", []workflowRun{with(func(r *workflowRun) { r.Path = ".github/workflows/other.yml" })}, successfulJobs(), false},
		{"other branch", []workflowRun{with(func(r *workflowRun) { r.HeadBranch = "feature" })}, successfulJobs(), false},
		{"PR result", []workflowRun{with(func(r *workflowRun) { r.Event = "pull_request" })}, successfulJobs(), false},
		{"failed newer run", []workflowRun{successfulRun, with(func(r *workflowRun) { r.ID, r.Conclusion = 11, "failure" })}, successfulJobs(), false},
		{"missing compatibility gate", []workflowRun{successfulRun}, withoutGate, false},
		{"skipped compatibility gate", []workflowRun{successfulRun}, skippedGate, false},
		{"ambiguous gate", []workflowRun{successfulRun}, duplicateGate, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.github.runs, f.github.jobs = tt.runs, tt.jobs
			if err := f.run(t, "require-ci"); (err == nil) != tt.pass {
				t.Fatalf("require-ci = %v, want pass %v", err, tt.pass)
			}
		})
	}
}

func TestReleaseWaitsForNewestCIToComplete(t *testing.T) {
	for _, conclusion := range []string{"success", "failure", "cancelled"} { //nolint:misspell // reason: GitHub Actions uses the external conclusion "cancelled".
		t.Run(conclusion, func(t *testing.T) {
			f := newFixture(t)
			queued := successfulRun
			queued.ID, queued.Status, queued.Conclusion = 11, "queued", ""
			running := queued
			running.Status = "in_progress"
			completed := queued
			completed.Status, completed.Conclusion = "completed", conclusion
			f.github.runSnapshots = [][]workflowRun{
				{successfulRun, queued},
				{successfulRun, running},
				{successfulRun, completed},
			}
			ticks := make(chan time.Time, 2)
			ticks <- time.Time{}
			ticks <- time.Time{}
			target, err := f.app.target()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			run, err := f.app.waitForCI(ctx, target, ticks)
			if conclusion == "success" {
				if err != nil || run.ID != 11 || run.Status != "completed" {
					t.Fatalf("waitForCI = %+v, %v; want completed run 11", run, err)
				}
				if err := f.run(t, "require-ci"); err != nil {
					t.Fatalf("completed CI did not pass release gate: %v", err)
				}
			} else if !errors.Is(err, errRelease) || !strings.Contains(err.Error(), "completed/"+conclusion) {
				t.Fatalf("waitForCI = %v, want completed/%s failure", err, conclusion)
			}
		})
	}
}

type cancelOnWrite struct {
	cancel context.CancelFunc
}

func (w cancelOnWrite) Write(data []byte) (int, error) {
	w.cancel()
	return len(data), nil
}

func TestReleaseCancelsWhileWaitingForCI(t *testing.T) {
	for _, status := range []string{"queued", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			f := newFixture(t)
			f.github.runs[0].Status, f.github.runs[0].Conclusion = status, ""
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.app.stdout = cancelOnWrite{cancel: cancel}
			if err := f.app.run(ctx, []string{"require-ci"}); !errors.Is(err, context.Canceled) {
				t.Fatalf("require-ci = %v, want cancellation", err)
			}
		})
	}
}

func trackedIssue(url string) regressionIssue {
	issue := regressionIssue{
		Body:  "Compatibility regression\n\n<!-- aht-compatibility:grok -->\n",
		State: "open", HTMLURL: url,
	}
	issue.User.Login = issueCreator
	return issue
}

func TestReleaseRejectsOnlyOpenTrackedBotRegressionIssues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*regressionIssue)
		block  bool
	}{
		{"tracked bot issue", func(*regressionIssue) {}, true},
		{"closed issue", func(i *regressionIssue) { i.State = "closed" }, false},
		{"human issue", func(i *regressionIssue) { i.User.Login = "contributor" }, false},
		{"different bot", func(i *regressionIssue) { i.User.Login = "other[bot]" }, false},
		{"pull request", func(i *regressionIssue) { i.PullRequest = &struct{}{} }, false},
		{"untracked issue", func(i *regressionIssue) { i.Body = "A different problem" }, false},
		{"inline marker", func(i *regressionIssue) { i.Body = "Quoted <!-- aht-compatibility:grok -->" }, false},
		{"marker suffix", func(i *regressionIssue) { i.Body = "<!-- aht-compatibility:grok --> extra" }, false},
		{"invalid marker", func(i *regressionIssue) { i.Body = "<!-- aht-compatibility: -->" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			issue := trackedIssue("https://example.invalid/issues/1")
			tt.change(&issue)
			f.github.issues = []regressionIssue{issue}
			err := f.run(t, "require-ci")
			if tt.block {
				if !errors.Is(err, errRelease) || !strings.Contains(err.Error(), issue.HTMLURL) {
					t.Fatalf("require-ci = %v, want tracked regression URL", err)
				}
			} else if err != nil {
				t.Fatalf("untracked issue prevented release: %v", err)
			}
		})
	}
}

func TestReleaseListsEveryTrackedRegressionURL(t *testing.T) {
	f := newFixture(t)
	f.github.issues = []regressionIssue{
		trackedIssue("https://example.invalid/issues/1"),
		trackedIssue("https://example.invalid/issues/2"),
	}
	err := f.run(t, "require-ci")
	if !errors.Is(err, errRelease) {
		t.Fatalf("require-ci = %v, want tracked regression failure", err)
	}
	for _, url := range []string{"https://example.invalid/issues/1", "https://example.invalid/issues/2"} {
		if !strings.Contains(err.Error(), url) {
			t.Errorf("regression failure %q lacks %s", err, url)
		}
	}
}

func TestReleasePropagatesGateAPIErrors(t *testing.T) {
	for _, path := range []string{
		"/repos/fixture/aht/actions/workflows/ci.yml/runs",
		"/repos/fixture/aht/actions/runs/10/jobs",
		"/repos/fixture/aht/issues",
	} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			f.github.fail["GET "+path] = failure{status: http.StatusForbidden}
			if err := f.run(t, "require-ci"); !githubapi.HasStatus(err, http.StatusForbidden) {
				t.Fatalf("require-ci = %v, want forbidden API failure", err)
			}
		})
	}
}

type paginationCase struct {
	name     string
	path     string
	pages    [2]any
	wantText string
}

func paginationCases() []paginationCase {
	newerFailure := successfulRun
	newerFailure.ID, newerFailure.Conclusion = 11, "failure"
	return []paginationCase{
		{
			name: "newest run is on second page",
			path: "/repos/fixture/aht/actions/workflows/ci.yml/runs",
			pages: [2]any{
				map[string]any{"workflow_runs": []workflowRun{successfulRun}},
				map[string]any{"workflow_runs": []workflowRun{newerFailure}},
			},
			wantText: "completed/failure",
		},
		{
			name: "required jobs span pages",
			path: "/repos/fixture/aht/actions/runs/10/jobs",
			pages: [2]any{
				map[string]any{"jobs": successfulJobs()[:2]},
				map[string]any{"jobs": successfulJobs()[2:]},
			},
		},
		{
			name: "regression is on second page",
			path: "/repos/fixture/aht/issues",
			pages: [2]any{
				[]regressionIssue{},
				[]regressionIssue{trackedIssue("https://example.invalid/issues/2")},
			},
			wantText: "https://example.invalid/issues/2",
		},
	}
}

func (tc paginationCase) handler(t *testing.T, failSecond bool) http.Handler {
	t.Helper()
	github := &fakeGitHub{runs: []workflowRun{successfulRun}, jobs: successfulJobs()}
	fallback := github.handler(t)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("release gate attempted a GitHub write: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != tc.path {
			fallback.ServeHTTP(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			if failSecond {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeJSON(w, tc.pages[1])
			return
		}
		next := *r.URL
		query := next.Query()
		query.Set("page", "2")
		next.RawQuery = query.Encode()
		w.Header().Set("Link", fmt.Sprintf("<http://%s%s>; rel=\"next\"", r.Host, next.String()))
		writeJSON(w, tc.pages[0])
	})
}

func runPaginatedGate(t *testing.T, tc paginationCase, failSecond bool) error {
	t.Helper()
	server := httptest.NewServer(tc.handler(t, failSecond))
	t.Cleanup(server.Close)
	app := application{api: githubapi.New(server.URL, ""), stdout: io.Discard, getenv: func(key string) string { return fixtureEnv[key] }}
	return app.run(t.Context(), []string{"require-ci"})
}

func TestReleaseGateFollowsPagination(t *testing.T) {
	for _, tc := range paginationCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := runPaginatedGate(t, tc, false)
			if tc.wantText == "" {
				if err != nil {
					t.Fatalf("jobs split over two pages prevented release: %v", err)
				}
				return
			}
			if !errors.Is(err, errRelease) || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("require-ci = %v, want release failure containing %q", err, tc.wantText)
			}
		})
	}
}

func TestReleaseGateFailsOnLaterPageErrors(t *testing.T) {
	for _, tc := range paginationCases() {
		t.Run(tc.name, func(t *testing.T) {
			if err := runPaginatedGate(t, tc, true); !githubapi.HasStatus(err, http.StatusForbidden) {
				t.Fatalf("require-ci = %v, want later-page API failure", err)
			}
		})
	}
}

func TestReleaseRequiresTagAndCommitIdentity(t *testing.T) {
	for key, value := range map[string]string{
		"GITHUB_REF":        "refs/heads/master",
		"AHT_RELEASE_SHA":   "main",
		"GITHUB_RUN_ID":     "",
		"GITHUB_REPOSITORY": "fixture",
	} {
		t.Run(key, func(t *testing.T) {
			f := newFixture(t)
			f.setEnv(key, value)
			if err := f.run(t, "cleanup-draft"); err == nil {
				t.Fatalf("accepted %s=%q", key, value)
			}
		})
	}
}

func TestDraftRetriesReuseOwnershipPreserveNotesAndCleanUp(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatal(err)
	}
	if f.output(t, "release-id") != "7" || f.output(t, "published") != "false" {
		t.Fatalf("outputs = %q", f.output(t, "release-id")+"/"+f.output(t, "published"))
	}
	marker := "<!-- aht-release:fixture/aht:42:" + fixtureSHA + " -->"
	if want := "## Features\n\nA change\n\n" + marker + "\n"; f.github.release.Body != want {
		t.Fatalf("body = %q, want %q", f.github.release.Body, want)
	}
	f.github.fail["POST /repos/fixture/aht/releases"] = failure{status: http.StatusInternalServerError, apply: false}
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatalf("retry did not reuse the draft: %v", err)
	}
	if err := f.run(t, "cleanup-draft"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.github.deleted, []int64{7}) {
		t.Fatalf("deleted = %v", f.github.deleted)
	}
}

func TestPrereleaseTagsCreatePrereleases(t *testing.T) {
	var created newRelease
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&created)
		writeJSON(w, release{ID: 1, TagName: created.TagName, TargetCommitish: created.TargetCommitish, Draft: true, Body: created.Body})
	}))
	t.Cleanup(server.Close)
	env := map[string]string{"GITHUB_REF": "refs/tags/v1.2.3-rc.1", "AHT_RELEASE_NOTES": writeNotes(t), "GITHUB_OUTPUT": filepath.Join(t.TempDir(), "out")}
	app := application{api: githubapi.New(server.URL, ""), stdout: io.Discard, getenv: func(key string) string { return cmp.Or(env[key], fixtureEnv[key]) }}
	if err := app.run(t.Context(), []string{"prepare-draft"}); err != nil || !created.Prerelease || created.TagName != "v1.2.3-rc.1" {
		t.Fatalf("created = %+v, %v", created, err)
	}
}

func TestCleanupReconcilesCreationWhoseResponseWasLost(t *testing.T) {
	f := newFixture(t)
	f.github.fail["POST /repos/fixture/aht/releases"] = failure{status: http.StatusBadGateway, apply: true}
	if err := f.run(t, "prepare-draft"); err == nil {
		t.Fatal("lost creation response reported success")
	}
	if f.output(t, "release-id") != "" {
		t.Fatal("release id recorded for an unconfirmed creation")
	}
	if err := f.run(t, "cleanup-draft"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.github.deleted, []int64{7}) {
		t.Fatalf("deleted = %v", f.github.deleted)
	}
}

func TestForeignAndPublishedReleasesCannotBeDeleted(t *testing.T) {
	tests := []struct {
		name   string
		change func(*release)
	}{
		{"published", func(r *release) { r.Draft = false }},
		{"other commit", func(r *release) { r.TargetCommitish = strings.Repeat("b", 40) }},
		{"other run", func(r *release) { r.Body = "another run" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if err := f.run(t, "prepare-draft"); err != nil {
				t.Fatal(err)
			}
			tt.change(f.github.release)
			if err := f.run(t, "cleanup-draft"); err != nil {
				t.Fatal(err)
			}
			if len(f.github.deleted) != 0 {
				t.Fatalf("deleted = %v", f.github.deleted)
			}
			if f.github.release.Draft {
				if err := f.run(t, "prepare-draft"); err == nil || !strings.Contains(err.Error(), "another run") {
					t.Fatalf("prepare-draft on a foreign release = %v", err)
				}
			}
		})
	}
}

func TestLostPublicationResponseAndRerunsPreserveThePublishedRelease(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatal(err)
	}
	f.setEnv("AHT_RELEASE_ID", "7")
	f.github.fail["PATCH /repos/fixture/aht/releases/7"] = failure{status: http.StatusBadGateway, apply: true}
	if err := f.run(t, "publish-draft"); err != nil {
		t.Fatalf("confirmed publication reported failure: %v", err)
	}
	if err := f.run(t, "cleanup-draft"); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatal(err)
	}
	if f.output(t, "published") != "true" || len(f.github.deleted) != 0 {
		t.Fatalf("published = %q, deleted = %v", f.output(t, "published"), f.github.deleted)
	}
}

func TestPublicationFailureRemainsFailureAndDraftIsRemovable(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatal(err)
	}
	f.setEnv("AHT_RELEASE_ID", "7")
	f.github.fail["PATCH /repos/fixture/aht/releases/7"] = failure{status: http.StatusServiceUnavailable, apply: false}
	if err := f.run(t, "publish-draft"); !githubapi.HasStatus(err, http.StatusServiceUnavailable) {
		t.Fatalf("publish-draft = %v", err)
	}
	if err := f.run(t, "cleanup-draft"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.github.deleted, []int64{7}) {
		t.Fatalf("deleted = %v", f.github.deleted)
	}
}

func TestPublishRefusesReleaseOwnedByAnotherRun(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t, "prepare-draft"); err != nil {
		t.Fatal(err)
	}
	f.github.release.Body = "another run"
	f.setEnv("AHT_RELEASE_ID", strconv.Itoa(7))
	if err := f.run(t, "publish-draft"); err == nil || !f.github.release.Draft {
		t.Fatalf("published a foreign release: %v", err)
	}
}

func TestUnauthorizedLookupIsNotAnAbsentDraft(t *testing.T) {
	f := newFixture(t)
	f.github.fail["GET /repos/fixture/aht/releases/tags/v1.2.3"] = failure{status: http.StatusForbidden, apply: false}
	for _, command := range []string{"cleanup-draft", "prepare-draft"} {
		if err := f.run(t, command); !githubapi.HasStatus(err, http.StatusForbidden) {
			t.Fatalf("%s = %v", command, err)
		}
	}
	if f.github.release != nil || len(f.github.deleted) != 0 {
		t.Fatalf("release = %+v, deleted = %v", f.github.release, f.github.deleted)
	}
}

func TestUnknownCommandShowsUsage(t *testing.T) {
	f := newFixture(t)
	if err := f.run(t, "publish"); err == nil || !strings.Contains(err.Error(), usage) {
		t.Fatalf("error = %v", fmt.Sprint(err))
	}
}
