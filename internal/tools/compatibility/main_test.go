package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResultRejectsVersionDrift(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "codex.log"), []byte("current codex: codex-cli 0.154.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"AHT_COMPAT_WORK": directory, "AHT_COMPAT_HARNESS": "codex", "AHT_COMPAT_VERSION": "0.153.4", "AHT_INSTALL_OUTCOME": "success", "AHT_TEST_OUTCOME": "success"}
	app := application{getenv: func(key string) string { return env[key] }, stdout: io.Discard, stderr: io.Discard}
	if err := app.run(t.Context(), []string{"result"}); err == nil {
		t.Fatal("version drift passed")
	}
	var result hostResult
	if err := readJSON(filepath.Join(directory, "codex-result.json"), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "infrastructure" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResultRemainsRetryableWhenLogMissing(t *testing.T) {
	directory := t.TempDir()
	env := map[string]string{"AHT_COMPAT_WORK": directory, "AHT_COMPAT_HARNESS": "codex", "AHT_COMPAT_VERSION": "0.153.4", "AHT_INSTALL_OUTCOME": "success", "AHT_TEST_OUTCOME": "success"}
	app := application{getenv: func(key string) string { return env[key] }, stdout: io.Discard, stderr: io.Discard}
	if err := app.run(t.Context(), []string{"result"}); err == nil {
		t.Fatal("missing log passed")
	}
	var result hostResult
	if err := readJSON(filepath.Join(directory, "codex-result.json"), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "infrastructure" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorkflowOutputsAndStateRoundTrip(t *testing.T) {
	directory := t.TempDir()
	var saved atomic.Pointer[[]byte]
	server := compatibilityStateServer(t, &saved)
	t.Cleanup(server.Close)
	output := filepath.Join(directory, "output")
	env := map[string]string{"AHT_COMPAT_WORK": directory, "GITHUB_REPOSITORY": "owner/repo", "AHT_DEFAULT_BRANCH": "master", "GITHUB_RUN_ID": "20", "GITHUB_SERVER_URL": "https://github.com", "AHT_COMPAT_SELECTION": "droid", "GITHUB_OUTPUT": output}
	var stdout bytes.Buffer
	app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: io.Discard}
	runTestCommand(t, app, "detect")
	var plan releasePlan
	if err := readJSON(filepath.Join(directory, "plan.json"), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Matrix.Include) != 1 || plan.Matrix.Include[0].Harness != "droid" {
		t.Fatalf("plan = %+v", plan)
	}
	if err := app.writeJSON("droid-result.json", hostResult{Harness: "droid", Version: "1.2.3", Outcome: "failure"}); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, app, "finish")
	assertOutputs(t, output, "changed=true\n", "failed=true\n")
	assertOpenIssues(t, directory, []string{"droid"})
	stateData := readTestFile(t, filepath.Join(directory, "state.json"))
	archive := stateZip(t, stateData)
	saved.Store(&archive)
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, app, "detect")
	assertOutputs(t, output, "matrix={\"include\":[]}\n", "changed=false\n")
	runTestCommand(t, app, "finish")
	// The known regression stays tracked by its issue instead of failing again.
	assertOutputs(t, output, "failed=false\n")
	assertOpenIssues(t, directory, []string{"droid"})
}

func compatibilityStateServer(t *testing.T, saved *atomic.Pointer[[]byte]) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github/repos/owner/repo/actions/artifacts":
			if saved.Load() == nil {
				_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"artifacts":[{"id":1,"workflow_run":{"id":10,"head_branch":"master"}}]}`)
		case "/github/repos/owner/repo/actions/runs/10":
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/compatibility-releases.yml","event":"schedule"}`)
		case "/github/repos/owner/repo/actions/artifacts/1/zip":
			_, _ = w.Write(*saved.Load())
		case "/npm/droid/latest":
			_, _ = fmt.Fprint(w, `{"name":"droid","version":"1.2.3"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server
}

func TestDetectionRechecksChangedBinaryRevision(t *testing.T) {
	for _, test := range []struct {
		name     string
		previous string
		current  string
		selected bool
	}{
		{name: "new binary", previous: "old-release", current: "new-release", selected: true},
		{name: "same binary", previous: "new-release", current: "new-release", selected: false},
		{name: "unrecorded binary", current: "new-release", selected: true},
		{name: "unspecified binary", previous: "old-release", selected: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			var saved atomic.Pointer[[]byte]
			archive := stateZip(t, fmt.Sprintf(`{"schema":2,"harnesses":{"droid":{"source":%q,"version":"1.2.3","outcome":"success","revision":%q}},"successful":{}}`, testHarness(t, "droid").sourceKey(), test.previous))
			saved.Store(&archive)
			server := compatibilityStateServer(t, &saved)
			t.Cleanup(server.Close)
			env := map[string]string{"AHT_COMPAT_WORK": directory, "GITHUB_REPOSITORY": "owner/repo", "AHT_DEFAULT_BRANCH": "master", "GITHUB_RUN_ID": "20", "AHT_COMPAT_SELECTION": "droid", "AHT_COMPAT_REVISION": test.current}
			var stdout bytes.Buffer
			app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: io.Discard}
			runTestCommand(t, app, "detect")
			var plan releasePlan
			if err := readJSON(filepath.Join(directory, "plan.json"), &plan); err != nil {
				t.Fatal(err)
			}
			want := []candidate{}
			if test.selected {
				want = []candidate{{Harness: "droid", Version: "1.2.3"}}
			}
			if !slices.Equal(plan.Matrix.Include, want) {
				t.Fatalf("selected releases = %v, want %v", plan.Matrix.Include, want)
			}
		})
	}
}

func assertOpenIssues(t *testing.T, directory string, want []string) {
	t.Helper()
	var report issueReport
	if err := readJSON(filepath.Join(directory, "issues.json"), &report); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(report.Open))
	for _, item := range report.Open {
		got = append(got, item.Harness)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("open issues = %v, want %v", got, want)
	}
}

func TestTrackedIssues(t *testing.T) {
	grok, kimi, codex := testHarness(t, "grok"), testHarness(t, "kimi-code"), testHarness(t, "codex")
	state := emptyState()
	state.Harnesses["grok"] = checkedRelease{Source: grok.sourceKey(), Version: "1.0.41", Outcome: "failure", RunURL: "grok-run"}
	state.Successful["grok"] = checkedRelease{Source: grok.sourceKey(), Version: "1.0.39", Outcome: "success"}
	state.Harnesses["kimi-code"] = checkedRelease{Source: kimi.sourceKey(), Version: "1.52.0", Outcome: "failure", RunURL: "kimi-run"}
	state.Harnesses["codex"] = checkedRelease{Source: codex.sourceKey(), Version: "0.157.0", Outcome: "success", RunURL: "codex-run"}
	state.Harnesses["claude"] = checkedRelease{Source: testHarness(t, "claude").sourceKey(), Version: "2.1.0", Outcome: "infrastructure"}
	state.Harnesses["droid"] = checkedRelease{Source: "npm:old-package", Version: "1.0.0", Outcome: "failure"}
	want := issueReport{
		Open: []issueStatus{{Harness: "grok", Version: "1.0.41", Successful: "1.0.39", RunURL: "grok-run"}},
		Resolved: []issueStatus{
			{Harness: "codex", Version: "0.157.0", RunURL: "codex-run", Reason: "passed"},
			{Harness: "kimi-code", Version: "1.52.0", MaxVersion: kimi.MaxVersion, RunURL: "kimi-run", Reason: "above supported maximum"},
		},
	}
	got := trackedIssues(state)
	slices.SortFunc(got.Resolved, func(a, b issueStatus) int { return strings.Compare(a.Harness, b.Harness) })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %+v\nwant %+v", got, want)
	}
}

func TestNewRegressions(t *testing.T) {
	spec := testHarness(t, "grok")
	failed := checkedRelease{Source: spec.sourceKey(), Version: "1.0.41", Outcome: "failure"}
	tests := []struct {
		name     string
		previous checkedRelease
		want     []string
	}{
		{"after success", checkedRelease{Source: spec.sourceKey(), Version: "1.0.40", Outcome: "success"}, []string{"grok"}},
		{"first observation", checkedRelease{}, []string{"grok"}},
		{"after infrastructure retry", checkedRelease{Source: spec.sourceKey(), Version: "1.0.41", Outcome: "infrastructure"}, []string{"grok"}},
		{"already failing", checkedRelease{Source: spec.sourceKey(), Version: "1.0.40", Outcome: "failure"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous, next := emptyState(), emptyState()
			if tt.previous.Source != "" {
				previous.Harnesses["grok"] = tt.previous
			}
			next.Harnesses["grok"] = failed
			if got := newRegressions(previous, next); !slices.Equal(got, tt.want) {
				t.Fatalf("new regressions = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResultOutcomes(t *testing.T) {
	for _, tc := range []struct{ install, test, want string }{
		{"success", "success", "success"},
		{"success", "failure", "failure"},
		{"failure", "skipped", "infrastructure"},
		{"failure", "failure", "infrastructure"},
		{"success", "cancelled", "incomplete"}, //nolint:misspell // GitHub Actions uses the external outcome "cancelled".
		{"cancelled", "skipped", "incomplete"}, //nolint:misspell // GitHub Actions uses the external outcome "cancelled".
		{"skipped", "skipped", "incomplete"},
	} {
		t.Run(tc.install+"/"+tc.test, func(t *testing.T) {
			if got := resultOutcome(tc.install, tc.test); got != tc.want {
				t.Fatalf("outcome = %s, want %s", got, tc.want)
			}
		})
	}
}

func runTestCommand(t *testing.T, app application, command string) {
	t.Helper()
	if err := app.run(t.Context(), []string{command}); err != nil {
		t.Fatal(err)
	}
}

func assertOutputs(t *testing.T, path string, expected ...string) {
	t.Helper()
	data := readTestFile(t, path)
	for _, value := range expected {
		if !strings.Contains(data, value) {
			t.Fatalf("output missing %q: %s", value, data)
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWeeklySelectsUnpinnedDistribution(t *testing.T) {
	var stdout bytes.Buffer
	app := application{getenv: func(string) string { return "" }, stdout: &stdout, stderr: io.Discard}
	if err := app.run(t.Context(), []string{"weekly"}); err != nil {
		t.Fatal(err)
	}
	var got matrix
	if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(stdout.String()), "matrix=")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Include) != 1 || got.Include[0].Harness != "cursor" {
		t.Fatalf("weekly matrix = %+v", got)
	}
}

func TestCanceledDetection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := detect(ctx, emptyState(), "droid", false, func(context.Context, harnessSpec) (string, error) { return "1.2.3", nil }); err == nil {
		t.Fatal("canceled detection succeeded")
	}
}

func TestAtomicWriteJSON(t *testing.T) {
	directory := t.TempDir()
	app := application{getenv: func(string) string { return directory }, stdout: io.Discard, stderr: io.Discard}
	data := map[string]string{"status": "ok"}
	if err := app.writeJSON("test.json", data); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := readJSON(filepath.Join(directory, "test.json"), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status"] != "ok" {
		t.Fatalf("decoded = %+v", decoded)
	}
	matches, err := filepath.Glob(filepath.Join(directory, "*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("leftover tmp files: %v, %v", matches, err)
	}
}
