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
	if result.Outcome != "failure" {
		t.Fatalf("result = %+v", result)
	}
}

func TestResultWritesFailureWhenLogMissing(t *testing.T) {
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
	if result.Outcome != "failure" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorkflowOutputsAndStateRoundTrip(t *testing.T) {
	directory := t.TempDir()
	var saved atomic.Pointer[[]byte]
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
	stateData := readTestFile(t, filepath.Join(directory, "state.json"))
	archive := stateZip(t, stateData)
	saved.Store(&archive)
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runTestCommand(t, app, "detect")
	assertOutputs(t, output, "matrix={\"include\":[]}\n", "changed=false\n")
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
