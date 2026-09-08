package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

type artifactSnapshot struct {
	info    os.FileInfo
	content []byte
}

// Backdate mtime so an unnecessary write is detected even on filesystems with
// coarse timestamp precision; SameFile also catches atomic replacement.
func snapshotUpgradeArtifacts(t *testing.T, paths []string) map[string]artifactSnapshot {
	t.Helper()
	snapshots := make(map[string]artifactSnapshot)
	for _, path := range paths {
		snapshotUpgradePath(t, path, snapshots)
	}
	return snapshots
}

func snapshotUpgradePath(t *testing.T, path string, snapshots map[string]artifactSnapshot) {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	err = fs.WalkDir(root.FS(), filepath.Base(path), func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		stamp := time.Unix(1234567890, 0)
		if err := root.Chtimes(relative, stamp, stamp); err != nil {
			return fmt.Errorf("backdate artifact: %w", err)
		}
		info, err := root.Stat(relative)
		if err != nil {
			return fmt.Errorf("stat artifact: %w", err)
		}
		var content []byte
		if !entry.IsDir() {
			content, err = root.ReadFile(relative)
			if err != nil {
				return fmt.Errorf("read artifact: %w", err)
			}
		}
		snapshots[filepath.Join(filepath.Dir(path), relative)] = artifactSnapshot{info: info, content: content}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertUpgradeArtifactsUntouched(t *testing.T, snapshots map[string]artifactSnapshot) {
	t.Helper()
	for path, before := range snapshots {
		after, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before.info, after) || !before.info.ModTime().Equal(after.ModTime()) || before.info.Mode() != after.Mode() {
			t.Fatalf("upgrade touched current artifact %s", path)
		}
		if after.IsDir() {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, before.content) {
			t.Fatalf("upgrade rewrote current artifact %s", path)
		}
	}
}

func TestCodexBinaryOnlyUpgradeLeavesReviewedHooksUntouched(t *testing.T) {
	isolateUpgradeHome(t)
	binary := filepath.Join(t.TempDir(), "aht")
	if err := os.WriteFile(binary, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := RunContext(t.Context(), Options{Harness: registry.HarnessCodex, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	// A reviewed file need not use the installer's JSON indentation. Reformatting
	// identical hook definitions must not create a new file for Codex to review.
	data, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed.Path, compact.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotUpgradeArtifacts(t, []string{installed.Path})
	if err := os.WriteFile(binary, []byte("updated binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := Upgrade(t.Context(), binary, false)
	if err != nil || len(results) != 1 {
		t.Fatalf("upgrade = %+v, %v", results, err)
	}
	if results[0].Changed || results[0].NextStep != "" {
		t.Fatalf("unchanged hooks requested review: %+v", results[0])
	}
	assertUpgradeArtifactsUntouched(t, before)
}
