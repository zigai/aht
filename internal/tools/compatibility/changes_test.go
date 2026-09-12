package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAffectedHosts(t *testing.T) {
	specs := catalog()
	all := make([]string, len(specs))
	for index, spec := range specs {
		all[index] = spec.ID
	}
	cases := []struct {
		name  string
		paths []string
		want  []string
	}{
		{name: "empty", paths: nil, want: []string{}},
		{name: "unrelated documentation", paths: []string{"README.md", "docs/library.md"}, want: []string{}},
		{name: "adapter", paths: []string{"internal/harness/codex/assets/hook.sh"}, want: []string{"codex"}},
		{name: "kimi directory differs from ID", paths: []string{"internal/harness/kimi/adapter.go"}, want: []string{"kimi-code"}},
		{name: "pi family", paths: []string{"internal/harness/pi/assets/aht-state.ts.tmpl"}, want: []string{"pi", "omp"}},
		{name: "omp family", paths: []string{"internal/harness/omp/adapter.go"}, want: []string{"pi", "omp"}},
		{name: "opencode family", paths: []string{"internal/harness/opencode/adapter.go"}, want: []string{"opencode", "kilo"}},
		{name: "kilo family", paths: []string{"internal/harness/kilo/adapter.go"}, want: []string{"opencode", "kilo"}},
		{name: "union deduplicates", paths: []string{"internal/harness/codex/adapter.go", "internal/harness/claude/manifest.go", "internal/harness/codex/manifest.go"}, want: []string{"claude", "codex"}},
		{name: "renamed adapter both sides", paths: []string{"internal/harness/codex/old.go", "internal/harness/cursor/new.go"}, want: []string{"codex", "cursor"}},
		{name: "shared template", paths: []string{"internal/harness/assets/typescript_queue.ts.tmpl"}, want: all},
		{name: "unknown adapter", paths: []string{"internal/harness/newhost/adapter.go"}, want: all},
	}
	for _, path := range []string{"internal/install/harness_plan.go", "pkg/registry/registry.go", "internal/brokerserver/server.go", "internal/observer/observer.go", "internal/cli/hook.go", "pkg/client/client.go", "go.mod", "go.sum", "Justfile", ".github/workflows/ci.yml", ".github/workflows/compatibility-host.yml", "internal/tools/compatibility/catalog.go", "internal/hostcompat/current_host_test.go", "docs/compatibility.md"} {
		t.Run(path, func(t *testing.T) {
			if got := affectedHosts([]string{path}); !slices.Equal(got, all) {
				t.Fatalf("affected hosts = %v, want %v", got, all)
			}
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := affectedHosts(tc.paths); !slices.Equal(got, tc.want) {
				t.Fatalf("affected hosts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChangesResolveWithoutReleaseState(t *testing.T) {
	dir := changeRepository(t)
	base := commitChange(t, dir, "README.md", "base")
	head := commitChange(t, dir, "internal/harness/codex/adapter.go", "changed")
	t.Chdir(dir)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/npm/@openai/codex/latest" {
			t.Errorf("unexpected request, especially release-state access: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, `{"name":"@openai/codex","version":"1.2.3"}`)
	}))
	t.Cleanup(server.Close)
	work := t.TempDir()
	state := filepath.Join(work, "state.json")
	if err := os.WriteFile(state, []byte("must remain untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GITHUB_EVENT_NAME": "push", "AHT_COMPAT_BASE": base, "AHT_COMPAT_HEAD": head, "AHT_COMPAT_WORK": work, "GITHUB_OUTPUT": filepath.Join(work, "output")}
	var stdout bytes.Buffer
	app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: io.Discard}
	for range 2 {
		stdout.Reset()
		runTestCommand(t, app, "changes")
		var plan releasePlan
		if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(plan.Matrix.Include, []candidate{{Harness: "codex", Version: "1.2.3"}}) {
			t.Fatalf("plan = %+v", plan)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("resolved %d times; want once each invocation despite unchanged version", requests.Load())
	}
	if got := readTestFile(t, state); got != "must remain untouched" {
		t.Fatalf("release state changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(work, "plan.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("persisted release plan: %v", err)
	}
}

func TestChangesMetadataFailurePublishesNoSelection(t *testing.T) {
	dir := changeRepository(t)
	base := commitChange(t, dir, "README.md", "base")
	head := commitChange(t, dir, "internal/harness/codex/adapter.go", "changed")
	t.Chdir(dir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	output := filepath.Join(t.TempDir(), "output")
	env := map[string]string{"GITHUB_EVENT_NAME": "push", "AHT_COMPAT_BASE": base, "AHT_COMPAT_HEAD": head, "GITHUB_OUTPUT": output}
	var stdout bytes.Buffer
	app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: io.Discard}
	if err := app.run(t.Context(), []string{"changes"}); !errors.Is(err, errCompatibility) {
		t.Fatalf("metadata failure = %v", err)
	}
	var plan releasePlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Observations) != 1 || plan.Observations[0].Error == "" {
		t.Fatalf("missing failure evidence: %+v", plan)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata failure published successful selection: %v", err)
	}
}

func TestChangedPathsComparison(t *testing.T) {
	dir := changeRepository(t)
	base := commitChange(t, dir, "internal/harness/cursor/old\nname.go", "content")
	runChangeGit(t, dir, "mv", "internal/harness/cursor/old\nname.go", "internal/harness/cursor/new.go")
	runChangeGit(t, dir, "commit", "-qm", "rename")
	head := runChangeGit(t, dir, "rev-parse", "HEAD")
	t.Chdir(dir)
	for _, event := range []string{"push", "pull_request", "merge_group"} {
		paths, err := changedPaths(t.Context(), event, base, head)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(paths, "internal/harness/cursor/old\nname.go") || !slices.Contains(paths, "internal/harness/cursor/new.go") {
			t.Fatalf("%s rename paths = %q", event, paths)
		}
	}
	paths, err := changedPaths(t.Context(), "push", strings.Repeat("0", 40), head)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(affectedHosts(paths), []string{"cursor"}) {
		t.Fatalf("new branch paths = %q", paths)
	}
	runChangeGit(t, dir, "rm", "internal/harness/cursor/new.go")
	runChangeGit(t, dir, "commit", "-qm", "delete")
	deleted := runChangeGit(t, dir, "rev-parse", "HEAD")
	paths, err = changedPaths(t.Context(), "push", head, deleted)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(affectedHosts(paths), []string{"cursor"}) {
		t.Fatalf("deleted paths = %q", paths)
	}
	// A target-branch-only shared change must not expand a PR's adapter diff.
	runChangeGit(t, dir, "checkout", "-qb", "target", base)
	target := commitChange(t, dir, "go.mod", "target only")
	paths, err = changedPaths(t.Context(), "pull_request", target, head)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(affectedHosts(paths), []string{"cursor"}) {
		t.Fatalf("PR included target-only paths: %q", paths)
	}
}

func TestCompatibilityUtilityProcess(t *testing.T) {
	if os.Getenv("AHT_COMPAT_TEST_PROCESS") == "1" {
		os.Args = []string{"compatibility", "changes"}
		main()
		os.Exit(0)
	}
	dir := changeRepository(t)
	base := commitChange(t, dir, "README.md", "base")
	docs := commitChange(t, dir, "docs/library.md", "docs only")
	head := commitChange(t, dir, "internal/harness/cursor/adapter.go", "cursor")
	for _, tc := range []struct {
		name, event, base, head string
		selected, failure       bool
	}{
		{name: "empty succeeds", event: "push", base: base, head: docs},
		{name: "cursor remains unpinned", event: "merge_group", base: docs, head: head, selected: true},
		{name: "missing base fails", event: "push", base: "missing", head: head, failure: true},
		{name: "missing head fails", event: "push", base: base, head: "missing", failure: true},
		{name: "unsupported event fails", event: "schedule", base: base, head: head, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			//nolint:gosec // re-executes current test binary with controlled flag in test helper
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCompatibilityUtilityProcess$")
			command.Dir = dir
			command.Env = append(os.Environ(), "AHT_COMPAT_TEST_PROCESS=1", "GITHUB_EVENT_NAME="+tc.event, "AHT_COMPAT_BASE="+tc.base, "AHT_COMPAT_HEAD="+tc.head, "GITHUB_OUTPUT="+output)
			assertChangeProcess(t, command, output, tc.selected, tc.failure)
		})
	}
}

func assertChangeProcess(t *testing.T, command *exec.Cmd, output string, selected, failure bool) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if failure {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || stderr.Len() == 0 {
			t.Fatalf("error=%v stderr=%q", err, stderr.String())
		}
		if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failure published selection: %v", statErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
	var plan releasePlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	want := []candidate{}
	if selected {
		want = append(want, candidate{Harness: "cursor", Version: ""})
	}
	if !slices.Equal(plan.Matrix.Include, want) {
		t.Fatalf("plan=%+v", plan)
	}
	assertOutputs(t, output, fmt.Sprintf("selected=%t\n", selected))
}

func changeRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runChangeGit(t, dir, "init", "-q")
	runChangeGit(t, dir, "config", "user.email", "compatibility@example.invalid")
	runChangeGit(t, dir, "config", "user.name", "Compatibility test")
	return dir
}

func commitChange(t *testing.T, dir, path, content string) string {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runChangeGit(t, dir, "add", "--", path)
	runChangeGit(t, dir, "commit", "-qm", "fixture")
	return runChangeGit(t, dir, "rev-parse", "HEAD")
}

func runChangeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}
