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
	specs := defaultCatalog
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
		{name: "unrelated only", paths: []string{
			"internal/architecture/conventions_test.go",
			"internal/tools/compatibility/catalog.go",
			"internal/tools/compatibility/changes_test.go",
			"internal/tools/release/release.go",
			"internal/tools/githubapi/client.go",
			"test/systemtest/tracking_workflow_test.go",
			"test/systemtest/stop_workflow_test.go",
			"test/systemtest/install_recipe_test.go",
			"test/systemtest/release_artifact_test.go",
			"test/README.md",
		}, want: []string{}},
		{name: "unrelated and adapter family", paths: []string{
			"internal/tools/compatibility/catalog.go",
			"test/systemtest/tracking_workflow_test.go",
			"internal/harness/pi/adapter.go",
			"internal/architecture/conventions_test.go",
		}, want: []string{"pi", "omp"}},
		{name: "unrelated and shared source", paths: []string{
			"internal/tools/release/release_test.go",
			"pkg/registry/registry.go",
		}, want: all},
		{name: "unrelated and unknown path", paths: []string{
			"test/systemtest/release_artifact_test.go",
			"internal/tools/newgenerator/main.go",
		}, want: all},
		{name: "adapter and real host lifecycle", paths: []string{
			"internal/harness/codex/adapter.go",
			"test/hostcompat/lifecycle_command_test.go",
		}, want: all},
		{name: "adapter unit test retains family", paths: []string{"internal/harness/omp/adapter_test.go"}, want: []string{"pi", "omp"}},
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
	for _, path := range []string{
		"internal/install/harness_plan.go",
		"internal/install/generated_runtime_integration_test.go",
		"internal/install/runtime_fixture_integration_test.go",
		"internal/install/testdata/node/omp-approval.mjs",
		"internal/install/testdata/python/hermes-session-end.py",
		"internal/harness/lifecycle_test.go",
		"pkg/registry/registry.go",
		"internal/brokerserver/server.go",
		"internal/observer/observer.go",
		"internal/cli/hook.go",
		"internal/config/config.go",
		"pkg/client/client.go",
		"go.mod",
		"go.sum",
		"Justfile",
		".goreleaser.yaml",
		".github/workflows/ci.yml",
		".github/workflows/compatibility-host.yml",
		"test/hostcompat/current_host_test.go",
		"test/hostcompat/lifecycle_observation_test.go",
		"test/hostcompat/model_fixture_test.go",
		"test/hostcompat/testdata/lifecycle.json",
		"internal/architecture/new_test.go",
		"internal/tools/newgenerator/main.go",
		"internal/tools/compatibility_extra/main.go",
		"test/systemtest/native_host_lifecycle_test.go",
		"test/systemtest/testdata/native-host.sh",
		"test/new_suite_test.go",
		"unknown.go",
	} {
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

func TestKnownGoodHonorsSourceAndMaximum(t *testing.T) {
	t.Parallel()
	spec := testHarness(t, "codex")
	spec.MaxVersion = "1.2.0"
	for _, scenario := range []struct {
		name      string
		source    string
		version   string
		want      string
		available bool
	}{
		{name: "caps proven release", source: spec.sourceKey(), version: "1.3.0", want: "1.2.0", available: true},
		{name: "ignores replaced distribution", source: "npm:retired-agent", version: "1.3.0", available: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			state := emptyState()
			state.Successful[spec.ID] = checkedRelease{Source: scenario.source, Version: scenario.version, Outcome: "success"}
			version, available, err := knownGood(spec, state)
			if err != nil || version != scenario.want || available != scenario.available {
				t.Fatalf("known good = %q, %t, %v", version, available, err)
			}
		})
	}
}

func TestChangesPinLastSuccessfulRelease(t *testing.T) {
	codex, kimi := testHarness(t, "codex"), testHarness(t, "kimi-code")
	successful := func(spec harnessSpec, version string) string {
		return fmt.Sprintf(`"%s":{"source":%q,"version":%q,"outcome":"success","run_url":"run"}`, spec.ID, spec.sourceKey(), version)
	}
	tests := []struct {
		name, path, successful string
		repository             string
		want                   candidate
		lookups                int32
		warning                bool
	}{
		{name: "pins proven release", path: "internal/harness/codex/adapter.go", successful: successful(codex, "1.2.0"), repository: "owner/repo", want: candidate{Harness: "codex", Version: "1.2.0"}},
		{name: "pins replacement release", path: "internal/harness/kimi/adapter.go", successful: successful(kimi, "2.1.1"), repository: "owner/repo", want: candidate{Harness: "kimi-code", Version: "2.1.1"}},
		{name: "resolves without history", path: "internal/harness/codex/adapter.go", repository: "owner/repo", want: candidate{Harness: "codex", Version: "1.2.3"}, lookups: 1},
		{name: "resolves when state is unavailable", path: "internal/harness/codex/adapter.go", want: candidate{Harness: "codex", Version: "1.2.3"}, lookups: 1, warning: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := changeRepository(t)
			base := commitChange(t, dir, "README.md", "base")
			head := commitChange(t, dir, tc.path, "changed")
			t.Chdir(dir)
			var lookups atomic.Int32
			server := successfulStateServer(t, tc.successful, &lookups)
			work := t.TempDir()
			env := map[string]string{"GITHUB_EVENT_NAME": "push", "AHT_COMPAT_BASE": base, "AHT_COMPAT_HEAD": head, "AHT_COMPAT_WORK": work, "GITHUB_OUTPUT": filepath.Join(work, "output"), "GITHUB_REPOSITORY": tc.repository, "AHT_DEFAULT_BRANCH": "master"}
			var stdout, stderr bytes.Buffer
			app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: &stderr}
			runTestCommand(t, app, "changes")
			var plan releasePlan
			if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(plan.Matrix.Include, []candidate{tc.want}) {
				t.Fatalf("plan = %+v", plan)
			}
			if lookups.Load() != tc.lookups {
				t.Fatalf("release lookups = %d, want %d", lookups.Load(), tc.lookups)
			}
			if got := strings.Contains(stderr.String(), "warning: release state unavailable"); got != tc.warning {
				t.Fatalf("warning = %v, stderr = %q", got, stderr.String())
			}
			// Change checks only read release history; they never publish it.
			if _, err := os.Stat(filepath.Join(work, "state.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("wrote release state: %v", err)
			}
			if _, err := os.Stat(filepath.Join(work, "plan.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("persisted release plan: %v", err)
			}
		})
	}
}

// successfulStateServer serves a trusted state artifact with the given
// successful records, or no artifact when successful is empty, and counts codex
// release lookups.
func successfulStateServer(t *testing.T, successful string, lookups *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github/repos/owner/repo/actions/artifacts":
			if successful == "" || r.URL.Query().Get("name") != stateArtifact {
				_, _ = fmt.Fprint(w, `{"artifacts":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"artifacts":[{"id":1,"workflow_run":{"id":10,"head_branch":"master"}}]}`)
		case "/github/repos/owner/repo/actions/runs/10":
			_, _ = fmt.Fprint(w, `{"path":".github/workflows/compatibility-releases.yml","event":"schedule"}`)
		case "/github/repos/owner/repo/actions/artifacts/1/zip":
			_, _ = w.Write(stateZip(t, `{"schema":2,"harnesses":{},"successful":{`+successful+`}}`))
		case "/npm/@openai/codex/latest":
			lookups.Add(1)
			_, _ = fmt.Fprint(w, `{"name":"@openai/codex","version":"1.2.3"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWeeklyChangesSkipReleaseState(t *testing.T) {
	dir := changeRepository(t)
	base := commitChange(t, dir, "README.md", "base")
	head := commitChange(t, dir, "internal/harness/cursor/adapter.go", "changed")
	t.Chdir(dir)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unpinned weekly harness made request %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	work := t.TempDir()
	env := map[string]string{"GITHUB_EVENT_NAME": "push", "AHT_COMPAT_BASE": base, "AHT_COMPAT_HEAD": head, "GITHUB_OUTPUT": filepath.Join(work, "output"), "GITHUB_REPOSITORY": "owner/repo", "AHT_DEFAULT_BRANCH": "master"}
	var stdout bytes.Buffer
	app := application{client: testClient(server), getenv: func(key string) string { return env[key] }, stdout: &stdout, stderr: io.Discard}
	runTestCommand(t, app, "changes")
	var plan releasePlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Matrix.Include, []candidate{{Harness: "cursor", Version: ""}}) {
		t.Fatalf("plan = %+v", plan)
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
	for _, event := range []string{"push", "pull_request"} {
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
		{name: "cursor remains unpinned", event: "pull_request", base: docs, head: head, selected: true},
		{name: "merge queue is unsupported", event: "merge_group", base: docs, head: head, failure: true},
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
