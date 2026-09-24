package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestInstallShimRequiresForceForForeignFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	path := filepath.Join(dir, "shims", "opencode")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating shim dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("writing foreign shim: %v", err)
	}

	_, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
		DryRun:       false,
		Force:        false,
		UseShim:      true,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged shim")
	}
}

func TestInstallShimWritesManagedScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)

	result, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
		DryRun:       false,
		Force:        false,
		UseShim:      true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected shim install to report changed")
	}
	if !strings.Contains(result.Snippet, managedMarker) {
		t.Fatalf("expected managed marker in snippet: %q", result.Snippet)
	}
	if result.Path != filepath.Join(dir, "shims", "opencode") {
		t.Fatalf("unexpected path %q", result.Path)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != shimFileMode {
		t.Fatalf("installed shim mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(shimFileMode))
	}
}

func TestInstallShimRepairsExecutableMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	options := Options{
		Harness: registry.Harness("opencode"), Binary: defaultBinary,
		TargetBinary: "/usr/bin/opencode", UseShim: true,
	}
	first, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(first.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := Inspect(registry.Harness("opencode"), defaultBinary)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != ArtifactStale {
		t.Fatalf("non-executable shim status = %q, want stale", status.Status)
	}
	repaired, err := Run(options)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.Changed {
		t.Fatal("mode repair was not reported as a change")
	}
	info, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != shimFileMode {
		t.Fatalf("repaired shim mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(shimFileMode))
	}
}

func TestInstallShimSupportsHarnessesMissingExitHooks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		harness      registry.Harness
		targetBinary string
	}{
		{name: "codex", harness: registry.Harness("codex"), targetBinary: "/usr/bin/codex"},
		{name: "agy", harness: registry.Harness("agy"), targetBinary: "/usr/bin/agy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(registry.StateDirEnv, dir)

			result, err := Run(Options{
				Harness:      tc.harness,
				Binary:       defaultBinary,
				TargetBinary: tc.targetBinary,
				DryRun:       false,
				Force:        false,
				UseShim:      true,
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if !result.Changed {
				t.Fatal("expected shim install to report changed")
			}
			if result.Path != filepath.Join(dir, "shims", string(tc.harness)) {
				t.Fatalf("unexpected path %q", result.Path)
			}
			requireTextContainsAll(t, result.Snippet, []string{
				managedMarker,
				"harness_bin=" + tc.targetBinary,
				"report " + string(tc.harness) + " --presence live --evidence process --pid \"$$\"",
				"report " + string(tc.harness) + " --presence gone --evidence process --pid \"$$\"",
			}, "shim script")
		})
	}
}

func TestInstallShimResolvesTargetOutsideManagedShimDir(t *testing.T) {
	dir := t.TempDir()
	realDir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	t.Setenv("PATH", filepath.Join(dir, "shims")+string(os.PathListSeparator)+realDir)

	shimPath := filepath.Join(dir, "shims", "opencode")
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o700); err != nil {
		t.Fatalf("creating shim dir: %v", err)
	}
	if err := writeExecutableTestFile(shimPath, []byte("#!/bin/sh\n# "+managedMarker+"\n")); err != nil {
		t.Fatalf("writing existing shim: %v", err)
	}
	realPath := filepath.Join(realDir, "opencode")
	if err := writeExecutableTestFile(realPath, []byte("#!/bin/sh\n")); err != nil {
		t.Fatalf("writing real harness binary: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: "",
		DryRun:       true,
		Force:        false,
		UseShim:      true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(result.Snippet, "harness_bin="+realPath) {
		t.Fatalf("expected shim to target real binary %q, got snippet: %s", realPath, result.Snippet)
	}
	if strings.Contains(result.Snippet, "harness_bin="+shimPath) {
		t.Fatalf("shim targets itself: %s", result.Snippet)
	}
}

func TestInstallShimRejectsManagedShimTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	shimPath := filepath.Join(dir, "shims", "opencode")

	_, err := Run(Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: shimPath,
		DryRun:       true,
		Force:        false,
		UseShim:      true,
	})
	if !errors.Is(err, errRecursiveShimTarget) {
		t.Fatalf("expected errRecursiveShimTarget, got %v", err)
	}
}
