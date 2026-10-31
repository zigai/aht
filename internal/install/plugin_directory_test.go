package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	plugin := pluginDirectoryInstall{dir: dir, markerFile: "", files: nil, snippetOrder: nil}

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

			if newPath == dir && strings.Contains(filepath.Base(oldPath), ".plugin.backup-") {
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

	plugin := pluginDirectoryInstall{dir: dir, markerFile: "", files: files, snippetOrder: nil}

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
		markerFile:   "",
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

	plugin := pluginDirectoryInstall{dir: dir, markerFile: "", files: files, snippetOrder: nil}

	changed, err := plugin.needsUpdate()
	if err != nil {
		t.Fatalf("plugin.needsUpdate returned error: %v", err)
	}

	if !changed {
		t.Fatal("expected stale nested generated file to require plugin directory replacement")
	}
}
