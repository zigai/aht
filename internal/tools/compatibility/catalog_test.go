package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMajorMinorSelection(t *testing.T) {
	spec := testHarness(t, "claude")
	tests := []struct {
		before, after string
		want          bool
	}{
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.99", false},
		{"1.2.3", "1.3.0", true},
		{"1.2.3", "1.3.2", true},
		{"1.2.3", "2.0.0", true},
		{"1.9.0", "1.10.0", true},
		{"0.9.0", "0.10.0", true},
		{"2.0.0", "1.99.0", false},
		{"v1.2.3", "1.2.4", false},
		{"2026.9.3", "2026.9.9", false},
		{"2026.9.3", "2026.10.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.before+"_to_"+tt.after, func(t *testing.T) {
			got, err := needsCheck(spec, tt.after, checkedRelease{Source: spec.sourceKey(), Version: tt.before}, false)
			if err != nil || got != tt.want {
				t.Fatalf("needsCheck = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
	for _, previous := range []checkedRelease{{}, {Source: "npm:old-package", Version: "1.2.3"}} {
		got, err := needsCheck(spec, "1.2.3", previous, false)
		if err != nil || !got {
			t.Fatalf("bootstrap = %v, %v", got, err)
		}
	}
	previous := checkedRelease{Source: spec.sourceKey(), Version: "1.2.3", Outcome: "failure"}
	got, err := needsCheck(spec, "1.2.4", previous, false)
	if err != nil || got {
		t.Fatalf("patch must not retry failure: %v, %v", got, err)
	}
	got, err = needsCheck(spec, "1.2.4", previous, true)
	if err != nil || !got {
		t.Fatalf("manual retry = %v, %v", got, err)
	}
}

func TestInvalidVersions(t *testing.T) {
	for _, value := range []string{"1.2.3-beta.1", "1.2.3+build", "latest", "--global", "1.2.3\nX=y", "$(id)", "01.2.3", "1.2", "", "18446744073709551616.0.0"} {
		if _, err := parseVersion(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestDetectionIsolatesSourceFailures(t *testing.T) {
	plan, err := detect(t.Context(), emptyState(), "all", false, func(_ context.Context, spec harnessSpec) (string, error) {
		if spec.ID == "claude" {
			return "", fmt.Errorf("%w: registry unavailable", errCompatibility)
		}
		return "1.2.3", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Matrix.Include) != len(catalog())-2 {
		t.Fatalf("matrix = %+v", plan.Matrix)
	}
	if !planHasErrors(plan) {
		t.Fatal("registry failure was hidden")
	}
	for _, item := range plan.Matrix.Include {
		if item.Harness == "claude" || item.Harness == "cursor" {
			t.Fatalf("unexpected harness: %+v", item)
		}
	}
}

func TestTargetedDetectionAndEmptyMatrix(t *testing.T) {
	state := emptyState()
	spec := testHarness(t, "droid")
	state.Harnesses[spec.ID] = checkedRelease{Source: spec.sourceKey(), Version: "1.2.3"}
	latest := func(_ context.Context, selected harnessSpec) (string, error) {
		if selected.ID != "droid" {
			return "", fmt.Errorf("%w: queried an unselected harness", errCompatibility)
		}
		return "1.2.4", nil
	}
	for _, force := range []bool{false, true} {
		plan, err := detect(t.Context(), state, "droid", force, latest)
		if err != nil {
			t.Fatal(err)
		}
		if planHasErrors(plan) || (len(plan.Matrix.Include) > 0) != force {
			t.Fatalf("force %v: %+v", force, plan)
		}
		if plan.Matrix.Include == nil {
			t.Fatal("empty matrix must serialize as [] rather than null")
		}
	}
	for _, selection := range []string{"cursor", "unknown"} {
		if _, err := detect(t.Context(), state, selection, false, latest); !errors.Is(err, errCompatibility) {
			t.Fatalf("selection %s: %v", selection, err)
		}
	}
}

func TestMergeCompletedAndIncompleteChecks(t *testing.T) {
	candidates := []candidate{{Harness: "claude", Version: "1.2.3"}, {Harness: "codex", Version: "1.2.3"}, {Harness: "pi", Version: "1.2.3"}, {Harness: "omp", Version: "1.2.3"}, {Harness: "droid", Version: "1.2.3"}}
	results := []hostResult{{Harness: "claude", Version: "1.2.3", Outcome: "success"}, {Harness: "codex", Version: "1.2.3", Outcome: "failure"}, {Harness: "pi", Version: "1.2.3", Outcome: "incomplete"}, {Harness: "omp", Version: "1.2.2", Outcome: "success"}}
	state := emptyState()
	next, incomplete, err := mergeResults(state, candidates, results, "https://github.com/example/repo/actions/runs/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Harnesses) != 0 {
		t.Fatal("mutated restored state")
	}
	if !reflect.DeepEqual(incomplete, []string{"pi", "omp", "droid"}) {
		t.Fatalf("incomplete = %v", incomplete)
	}
	if next.Harnesses["claude"].Outcome != "success" || next.Harnesses["codex"].Outcome != "failure" {
		t.Fatalf("results = %+v", next)
	}
	if next.Harnesses["codex"].RunURL == "" {
		t.Fatal("result lost originating run")
	}
}

func TestOldRerunPreservesNewerAndUnrelatedResults(t *testing.T) {
	state := emptyState()
	for id, version := range map[string]string{"claude": "2.0.0", "codex": "0.99.0"} {
		spec := testHarness(t, id)
		state.Harnesses[id] = checkedRelease{Source: spec.sourceKey(), Version: version, Outcome: "success"}
	}
	selected := []candidate{{Harness: "claude", Version: "1.9.0"}}
	results := []hostResult{{Harness: "claude", Version: "1.9.0", Outcome: "failure"}}
	next, _, err := mergeResults(state, selected, results, "old-run")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next, state) {
		t.Fatalf("old rerun changed state: %+v", next)
	}
}

func TestObservedVersionMatchesPin(t *testing.T) {
	if !checkedVersion("    test.go:1: current codex: codex-cli 0.153.4\n", "codex", "0.153.4") {
		t.Fatal("rejected codex version")
	}
	if !checkedVersion("    test.go:1: current goose: goose version 1.50.0\n", "goose", "v1.50.0") {
		t.Fatal("rejected goose version")
	}
	for _, value := range []string{"0.153.40", "0.153.4-beta", "0.153.4+dev", "0.153.5", "x0.153.4"} {
		if checkedVersion("current codex: "+value, "codex", "0.153.4") {
			t.Errorf("accepted %s", value)
		}
	}
	if checkedVersion("current claude: 0.153.4", "codex", "0.153.4") {
		t.Fatal("accepted wrong harness")
	}
}

func TestCatalogPartition(t *testing.T) {
	weekly, releases := []string{}, []string{}
	seen := map[string]bool{}
	for _, spec := range catalog() {
		if seen[spec.ID] {
			t.Fatalf("duplicate %s", spec.ID)
		}
		seen[spec.ID] = true
		if spec.Source == "weekly" {
			weekly = append(weekly, spec.ID)
		} else {
			releases = append(releases, spec.ID)
		}
	}
	if strings.Join(weekly, ",") != "cursor" || len(releases) != 15 {
		t.Fatalf("weekly=%v releases=%v", weekly, releases)
	}
}

func testHarness(t *testing.T, id string) harnessSpec {
	t.Helper()
	spec, err := findHarness(id)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
