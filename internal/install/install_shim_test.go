package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPersistedShimOwnershipAcrossManagement(t *testing.T) {
	for _, operation := range []string{"install", "status", "upgrade", "remove"} {
		t.Run(operation, func(t *testing.T) {
			isolateUpgradeHome(t)

			path := filepath.Join(registry.DefaultStateDir(), "shims", "opencode")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}

			content := "#!/bin/sh\n# " + harness.ManagedMarker + "\n" +
				"# AHT_INTEGRATION_ID=opencode-shim\n# AHT_INTEGRATION_VERSION=1\n" +
				"# AHT_SOURCE=opencode-shim\naht_bin='/bin/old-aht'\nharness_bin='/custom/native'\n"
			if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
				t.Fatal(err)
			}

			opts := Options{
				Harness: registry.Harness("opencode"), Binary: "/bin/new-aht",
				TargetBinary: "/custom/native", UseShim: true,
			}

			switch operation {
			case "install":
				result, err := Run(t.Context(), opts)
				if err != nil || !result.Changed {
					t.Fatalf("install = %+v, %v", result, err)
				}
			case "status":
				status, err := Inspect(t.Context(), opts.Harness, opts.Binary)
				if err != nil || status.Status != ArtifactStale {
					t.Fatalf("status = %+v, %v", status, err)
				}
			case "upgrade":
				results, err := Upgrade(t.Context(), opts.Binary, false)
				if err != nil || len(results) != 1 || results[0].Error != "" ||
					!results[0].Changed {
					t.Fatalf("upgrade = %+v, %v", results, err)
				}
			case "remove":
				result, err := Remove(t.Context(), opts)
				if err != nil || !result.Changed {
					t.Fatalf("remove = %+v, %v", result, err)
				}

				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("removed shim still exists: %v", err)
				}

				return
			}

			if operation != "status" {
				data, err := os.ReadFile(path)
				if err != nil ||
					!strings.Contains(string(data), "AHT_INTEGRATION_ID=opencode-shim\n") {
					t.Fatalf("updated shim = %s, %v", data, err)
				}

				target, exists, err := installedShim(opts.Harness)
				if err != nil || !exists || target != "/custom/native" {
					t.Fatalf("shim target = %q, %v, %v", target, exists, err)
				}
			}
		})
	}
}

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

	_, err := Run(t.Context(), Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
		UseShim:      true,
	})
	if err == nil {
		t.Fatal("expected error for unmanaged shim")
	}
}

func TestInstallShimWritesManagedScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)

	result, err := Run(t.Context(), Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: "/usr/bin/opencode",
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
		t.Fatalf(
			"installed shim mode = %#o, want %#o",
			info.Mode().Perm(),
			os.FileMode(shimFileMode),
		)
	}
}

func TestInstallShimRepairsExecutableMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)

	options := Options{
		Harness: registry.Harness("opencode"), Binary: defaultBinary,
		TargetBinary: "/usr/bin/opencode", UseShim: true,
	}

	first, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(first.Path, 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := Inspect(t.Context(), registry.Harness("opencode"), defaultBinary)
	if err != nil {
		t.Fatal(err)
	}

	if status.Status != ArtifactStale {
		t.Fatalf("non-executable shim status = %q, want stale", status.Status)
	}

	repaired, err := Run(t.Context(), options)
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
		t.Fatalf(
			"repaired shim mode = %#o, want %#o",
			info.Mode().Perm(),
			os.FileMode(shimFileMode),
		)
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

			result, err := Run(t.Context(), Options{
				Harness:      tc.harness,
				Binary:       defaultBinary,
				TargetBinary: tc.targetBinary,
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

	if err := writeExecutableTestFile(
		shimPath,
		[]byte("#!/bin/sh\n# "+managedMarker+"\n# AHT_INTEGRATION_ID=opencode-shim\n"),
	); err != nil {
		t.Fatalf("writing existing shim: %v", err)
	}

	realPath := filepath.Join(realDir, "opencode")
	if err := writeExecutableTestFile(realPath, []byte("#!/bin/sh\n")); err != nil {
		t.Fatalf("writing real harness binary: %v", err)
	}

	result, err := Run(t.Context(), Options{
		Harness: registry.Harness("opencode"),
		Binary:  defaultBinary,
		DryRun:  true,
		UseShim: true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(result.Snippet, "harness_bin="+realPath) {
		t.Fatalf(
			"expected shim to target real binary %q, got snippet: %s",
			realPath,
			result.Snippet,
		)
	}

	if strings.Contains(result.Snippet, "harness_bin="+shimPath) {
		t.Fatalf("shim targets itself: %s", result.Snippet)
	}
}

func TestInstallShimRejectsManagedShimTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.StateDirEnv, dir)
	shimPath := filepath.Join(dir, "shims", "opencode")

	_, err := Run(t.Context(), Options{
		Harness:      registry.Harness("opencode"),
		Binary:       defaultBinary,
		TargetBinary: shimPath,
		DryRun:       true,
		UseShim:      true,
	})
	if !errors.Is(err, errRecursiveShimTarget) {
		t.Fatalf("expected errRecursiveShimTarget, got %v", err)
	}
}

func writeExecutableTestFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing executable test file: %w", err)
	}

	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("marking executable test file executable: %w", err)
	}

	return nil
}
