package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errInstallStageTest  = errors.New("install staged plugin failed")
	errRestoreBackupTest = errors.New("restore plugin backup failed")
	errRemoveBackupTest  = errors.New("remove plugin backup failed")
)

func TestReplacePluginDirectoryRollbackRestoresExistingDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	dir := filepath.Join(root, "plugin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating plugin dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("writing old plugin file: %v", err)
	}

	staged := filepath.Join(root, "staged")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatalf("creating staged dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(staged, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatalf("writing staged plugin file: %v", err)
	}

	plugin := pluginDirectoryInstall{dir: dir, files: nil, snippetOrder: nil}

	rollback, _, err := plugin.replace(staged)
	if err != nil {
		t.Fatalf("plugin.replace returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("expected staged plugin to be installed: %v", err)
	}

	if err := rollback(); err != nil {
		t.Fatalf("rollback returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatalf("expected old plugin to be restored: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected staged plugin file to be removed, stat err=%v", err)
	}
}

func TestReplacePluginDirectoryReportsInstallAndRestoreFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "plugin")
	staged := filepath.Join(root, "staged")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}

	plugin := pluginDirectoryInstall{
		dir: dir,
		renameFile: func(oldPath string, newPath string) error {
			if oldPath == staged {
				return errInstallStageTest
			}

			if newPath == dir && strings.Contains(oldPath, ".plugin.backup-") {
				return errRestoreBackupTest
			}

			return os.Rename(oldPath, newPath)
		},
	}

	_, _, err := plugin.replace(staged)
	if !errors.Is(err, errInstallStageTest) || !errors.Is(err, errRestoreBackupTest) {
		t.Fatalf("replace error = %v, want install and restore failures", err)
	}

	if !strings.Contains(err.Error(), ".plugin.backup-") {
		t.Fatalf("replace error omits retained backup path: %v", err)
	}
}

func TestReplacePluginDirectoryReportsBackupRemovalFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "plugin")
	staged := filepath.Join(root, "staged")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}

	plugin := pluginDirectoryInstall{
		dir: dir,
		removeTree: func(path string) error {
			if strings.Contains(filepath.Base(path), ".plugin.backup-") {
				return errRemoveBackupTest
			}

			return os.RemoveAll(path)
		},
	}

	_, commit, err := plugin.replace(staged)
	if err != nil {
		t.Fatal(err)
	}

	if err := commit(); !errors.Is(err, errRemoveBackupTest) ||
		!strings.Contains(err.Error(), ".plugin.backup-") {
		t.Fatalf("commit error = %v, want retained backup path", err)
	}
}

func TestPluginDirectoryNeedsUpdateDetectsStaleFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{"plugin.json": "{}\n"}

	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writing expected plugin file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "stale.js"), []byte("old"), 0o600); err != nil {
		t.Fatalf("writing stale plugin file: %v", err)
	}

	plugin := pluginDirectoryInstall{dir: dir, files: files, snippetOrder: nil}

	changed, err := plugin.needsUpdate()
	if err != nil {
		t.Fatalf("plugin.needsUpdate returned error: %v", err)
	}

	if !changed {
		t.Fatal("expected stale generated file to require plugin directory replacement")
	}
}

func TestPluginDirectorySupportsNestedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"plugin.json":       "{}\n",
		"hooks/hooks.json":  "{\"hooks\":{}}\n",
		"scripts/report.sh": "#!/bin/sh\n",
	}
	plugin := pluginDirectoryInstall{
		dir:          filepath.Join(root, "plugin"),
		files:        files,
		snippetOrder: []string{"plugin.json", "hooks/hooks.json", "scripts/report.sh"},
	}

	staged, err := plugin.stage()
	if err != nil {
		t.Fatalf("plugin.stage returned error: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(staged)
	}()

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(staged, name))
		if err != nil {
			t.Fatalf("reading staged file %s: %v", name, err)
		}

		if string(got) != want {
			t.Fatalf("expected %s content %q, got %q", name, want, got)
		}
	}
}

func TestPluginDirectoryNeedsUpdateDetectsNestedStaleFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	files := map[string]string{
		"plugin.json":      "{}\n",
		"hooks/hooks.json": "{\"hooks\":{}}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("creating directory for %s: %v", name, err)
		}

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("writing expected plugin file %s: %v", name, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(dir, "hooks", "old.json"),
		[]byte("old"),
		0o600,
	); err != nil {
		t.Fatalf("writing stale nested plugin file: %v", err)
	}

	plugin := pluginDirectoryInstall{dir: dir, files: files, snippetOrder: nil}

	changed, err := plugin.needsUpdate()
	if err != nil {
		t.Fatalf("plugin.needsUpdate returned error: %v", err)
	}

	if !changed {
		t.Fatal("expected stale nested generated file to require plugin directory replacement")
	}
}

func TestReinstallAfterInterruptedPluginReplaceLeavesOnlyPluginDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	plan := harness.PluginDirectoryInstallPlan{
		Dir: filepath.Join(root, "plugin"),
		Files: []harness.RenderedFileInstallSpec{
			{Name: "plugin.json", Content: "{}\n"},
		},
		Identity: harness.ArtifactIdentity{ID: "test", Version: 1, Source: "test-plugin"},
	}

	if _, err := installPluginDirectory(
		t.Context(),
		Options{},
		registry.Harness("test"),
		plan,
	); err != nil {
		t.Fatal(err)
	}

	files, order, err := renderPluginFiles(plan)
	if err != nil {
		t.Fatal(err)
	}

	renames := 0
	interrupted := newPluginDirectoryInstall(plan, files, order)
	interrupted.renameFile = func(oldPath string, newPath string) error {
		renames++
		if renames == 2 {
			panic("process killed between backup and install renames")
		}

		return os.Rename(oldPath, newPath)
	}

	func() {
		defer func() { _ = recover() }()

		_, _, _ = interrupted.installStaged()
	}()

	if renames != 2 {
		t.Fatalf("interrupted install made %d renames, want the crash on the second", renames)
	}

	if _, err := installPluginDirectory(
		t.Context(),
		Options{},
		registry.Harness("test"),
		plan,
	); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	if len(names) != 1 || names[0] != "plugin" {
		t.Fatalf("plugin parent after reinstall = %q, want only plugin", names)
	}
}

func TestInstallIgnoresUnprovenPluginBackups(t *testing.T) {
	t.Parallel()

	for _, targetExists := range []bool{false, true} {
		name := "target missing"
		if targetExists {
			name = "target installed"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			plan := harness.PluginDirectoryInstallPlan{
				Dir: filepath.Join(root, "plugin"),
				Files: []harness.RenderedFileInstallSpec{
					{Name: "plugin.json", Content: "{}\n"},
				},
				Identity: harness.ArtifactIdentity{
					ID:      "test",
					Version: 1,
					Source:  "test-plugin",
				},
			}

			harnessID := registry.Harness("test")
			if targetExists {
				if _, err := installPluginDirectory(
					t.Context(),
					Options{},
					harnessID,
					plan,
				); err != nil {
					t.Fatal(err)
				}
			}

			for _, path := range []string{".plugin.backup-123", ".plugin.backup-456/original", ".plugin.stage-789"} {
				path := filepath.Join(root, path)
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(
					filepath.Join(path, "notes.txt"),
					[]byte("keep"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			for _, dryRun := range []bool{true, false} {
				result, err := installPluginDirectory(
					t.Context(),
					Options{DryRun: dryRun},
					harnessID,
					plan,
				)
				if err != nil || result.Changed == targetExists {
					t.Fatalf(
						"install (dry run %t) = %+v, %v; want Changed = %t",
						dryRun,
						result,
						err,
						!targetExists,
					)
				}
			}

			for _, path := range []string{".plugin.backup-123", ".plugin.backup-456/original", ".plugin.stage-789"} {
				data, err := os.ReadFile(filepath.Join(root, path, "notes.txt"))
				if err != nil || string(data) != "keep" {
					t.Errorf(
						"unproven recovery path %s: notes = %q, %v; want keep",
						path,
						data,
						err,
					)
				}
			}
		})
	}
}

func TestReinstallRestoresProvenMarkerlessBackupWithoutForce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	plan := harness.PluginDirectoryInstallPlan{
		Dir:      filepath.Join(root, "plugin"),
		Files:    []harness.RenderedFileInstallSpec{{Name: "plugin.json", Content: "{}\n"}},
		Identity: harness.ArtifactIdentity{ID: "test", Version: 1, Source: "test-plugin"},
	}
	if err := os.Mkdir(plan.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(plan.Dir, "original.txt"),
		[]byte("keep"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	// Simulate interruption of a forced replacement after moving the old
	// directory, but before the staged replacement becomes visible.
	plugin := newPluginDirectoryInstall(plan, nil, nil)

	backup, exists, err := plugin.backupCurrent(root)
	if err != nil || !exists {
		t.Fatalf("backing up markerless plugin = %q, %t, %v", backup, exists, err)
	}

	harnessID := registry.Harness("test")
	if _, err := installPluginDirectory(
		t.Context(),
		Options{DryRun: true},
		harnessID,
		plan,
	); !errors.Is(
		err,
		errForeignFile,
	) {
		t.Fatalf("dry run error = %v, want foreign-file refusal after projected recovery", err)
	}

	if _, err := os.Lstat(plan.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run changed the target: %v", err)
	}

	if _, err := installPluginDirectory(
		t.Context(),
		Options{},
		harnessID,
		plan,
	); !errors.Is(
		err,
		errForeignFile,
	) {
		t.Fatalf("real install error = %v, want foreign-file refusal after recovery", err)
	}

	data, err := os.ReadFile(filepath.Join(plan.Dir, "original.txt"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("recovered original file = %q, %v; want keep", data, err)
	}

	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left its own backup: %v", err)
	}
}

func TestReinstallRestoresManagedLegacyBackupWithoutRewriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	plan := harness.PluginDirectoryInstallPlan{
		Dir:      filepath.Join(root, "plugin"),
		Files:    []harness.RenderedFileInstallSpec{{Name: "plugin.json", Content: "{}\n"}},
		Identity: harness.ArtifactIdentity{ID: "test", Version: 1, Source: "test-plugin"},
	}

	harnessID := registry.Harness("test")
	if _, err := installPluginDirectory(t.Context(), Options{}, harnessID, plan); err != nil {
		t.Fatal(err)
	}

	backup := filepath.Join(root, ".plugin.backup-123")
	if err := os.Rename(plan.Dir, backup); err != nil {
		t.Fatal(err)
	}

	original, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}

	result, err := installPluginDirectory(t.Context(), Options{DryRun: true}, harnessID, plan)
	if err != nil || !result.Changed {
		t.Fatalf("preview of legacy backup recovery = %+v, %v; want Changed", result, err)
	}

	if _, err := os.Lstat(plan.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run changed the target: %v", err)
	}

	result, err = installPluginDirectory(t.Context(), Options{}, harnessID, plan)
	if err != nil || !result.Changed {
		t.Fatalf("legacy backup recovery = %+v, %v; want Changed", result, err)
	}

	current, err := os.Stat(plan.Dir)
	if err != nil || !os.SameFile(original, current) {
		t.Fatalf("legacy backup was not restored in place: %v", err)
	}
}
