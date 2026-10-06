package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestManageLocationsJSONReportsExistenceUnderTemporaryHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o750); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--json", "manage", "locations", "codex", "--project", projectDir}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
	}
	var locations []harness.Location
	if err := json.Unmarshal(stdout.Bytes(), &locations); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}

	for _, location := range locations {
		if location.Harness != registry.Harness("codex") {
			t.Errorf("unexpected harness in %+v", location)
		}
	}
	assertLocation(t, locations, harness.LocationKindSkills, harness.LocationScopeGlobal, filepath.Join(home, ".agents", "skills"), true)
	assertLocation(t, locations, harness.LocationKindSkills, harness.LocationScopeProject, filepath.Join(projectDir, ".agents", "skills"), false)
	assertLocation(t, locations, harness.LocationKindConfig, harness.LocationScopeProject, filepath.Join(projectDir, ".codex", "config.toml"), false)
}

func assertLocation(t *testing.T, locations []harness.Location, kind harness.LocationKind, scope harness.LocationScope, path string, exists bool) {
	t.Helper()
	for _, location := range locations {
		if location.Kind == kind && location.Scope == scope && location.Path == path {
			if location.Exists != exists {
				t.Errorf("%s exists = %t, want %t", path, location.Exists, exists)
			}
			return
		}
	}
	t.Errorf("no %s %s location at %s in %+v", scope, kind, path, locations)
}

func TestManageLocationsTableListsEveryHarnessByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "locations", "--project", t.TempDir()}, &stdout, &stderr); err != nil {
		t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
	}
	for _, id := range harness.Supported() {
		if !strings.Contains(stdout.String(), string(id)) {
			t.Errorf("table does not mention %s", id)
		}
	}
	for _, heading := range []string{"Harness", "Kind", "Scope", "Path", "Exists"} {
		if !strings.Contains(stdout.String(), heading) {
			t.Errorf("table does not have a %s column", heading)
		}
	}
}

func TestManageLocationsRejectsUnknownHarness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"manage", "locations", "claude", "no-such-harness"}, &stdout, &stderr)
	if !errors.Is(err, registry.ErrUnknownHarness) {
		t.Fatalf("error = %v, want ErrUnknownHarness", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("output before validation failed: %q", stdout.String())
	}
}

func TestManageLocationsUnknownHarnessMessageAndExitCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"manage", "locations", "no-such-harness"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitCodeUsage {
		t.Fatalf("exit code = %d, want %d", code, exitCodeUsage)
	}
	if !strings.Contains(stderr.String(), `unknown harness "no-such-harness"`) || strings.Count(stderr.String(), "unknown harness") != 1 {
		t.Fatalf("stderr = %q, want a single unknown harness message naming the argument", stderr.String())
	}
}

func TestManageLocationsJSONFieldNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--json", "manage", "locations", "codex", "--project", t.TempDir()}, &stdout, &stderr); err != nil {
		t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if len(rows) == 0 {
		t.Fatal("no locations reported")
	}
	want := []string{"exists", "harness", "kind", "path", "scope"}
	for _, row := range rows {
		if got := slices.Sorted(maps.Keys(row)); !slices.Equal(got, want) {
			t.Fatalf("fields = %v, want %v in %v", got, want, row)
		}
		if _, ok := row["exists"].(bool); !ok {
			t.Fatalf("exists = %#v, want a boolean in %v", row["exists"], row)
		}
	}
}

func projectLocationPaths(t *testing.T, args ...string) []string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), append([]string{"--json", "manage", "locations", "codex"}, args...), &stdout, &stderr); err != nil {
		t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
	}
	var locations []harness.Location
	if err := json.Unmarshal(stdout.Bytes(), &locations); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	var paths []string
	for _, location := range locations {
		if location.Scope == harness.LocationScopeProject {
			paths = append(paths, location.Path)
		}
	}
	return paths
}

func TestManageLocationsProjectDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(workDir)

	for name, test := range map[string]struct {
		args []string
		root string
	}{
		"defaults to the current directory": {args: nil, root: workDir},
		"resolves a relative directory":     {args: []string{"--project", filepath.Join("nested", "..", "sub")}, root: filepath.Join(workDir, "sub")},
	} {
		t.Run(name, func(t *testing.T) {
			paths := projectLocationPaths(t, test.args...)
			if len(paths) == 0 {
				t.Fatal("no project locations reported")
			}
			for _, path := range paths {
				if !strings.HasPrefix(path, test.root+string(filepath.Separator)) {
					t.Errorf("project location %q is outside %q", path, test.root)
				}
			}
		})
	}
}

func TestManageLocationsListsEachHarnessOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	count := func(args ...string) int {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := runTestCLI(context.Background(), append([]string{"--json", "manage", "locations", "--project", t.TempDir()}, args...), &stdout, &stderr); err != nil {
			t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
		}
		var locations []harness.Location
		if err := json.Unmarshal(stdout.Bytes(), &locations); err != nil {
			t.Fatalf("decode %q: %v", stdout.String(), err)
		}
		return len(locations)
	}

	single := count("kimi-code")
	if single == 0 {
		t.Fatal("no locations for kimi-code")
	}
	if got := count("kimi-code", "kimi-code", "kimi"); got != single {
		t.Fatalf("repeated and aliased arguments listed %d locations, want %d", got, single)
	}
}

func TestManageLocationsTableMarksExistingPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o750); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "locations", "codex", "--project", t.TempDir()}, &stdout, &stderr); err != nil {
		t.Fatalf("locations failed: %v; stderr=%s", err, stderr.String())
	}

	var existing, missing int
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "codex" {
			continue
		}
		switch fields[len(fields)-1] {
		case "yes":
			existing++
		case "no":
			missing++
		}
	}
	if existing != 1 || missing == 0 {
		t.Fatalf("table has %d existing and %d missing rows, want 1 existing and some missing:\n%s", existing, missing, stdout.String())
	}
}
