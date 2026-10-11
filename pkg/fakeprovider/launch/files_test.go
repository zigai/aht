package launch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

var errFixtureUpdate = errors.New("fixture update refused")

func TestApplyFilesMergesInOrderWithPermissions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "config", "nested", "app.json")
	var seen [][]byte
	record := func(suffix string) func([]byte) ([]byte, error) {
		return func(existing []byte) ([]byte, error) {
			seen = append(seen, existing)
			return append(append([]byte(nil), existing...), suffix...), nil
		}
	}
	files := []harness.LaunchFile{
		{Path: nested, Update: record("first\n")},
		{Path: nested, Update: record("second\n")},
	}
	if err := applyFiles(root, files); err != nil {
		t.Fatal(err)
	}
	if seen[0] != nil {
		t.Errorf("first update saw %q, want nil for a missing file", seen[0])
	}
	if string(seen[1]) != "first\n" {
		t.Errorf("second update saw %q, want the first update's output", seen[1])
	}
	if got := readText(t, nested); got != "first\nsecond\n" {
		t.Errorf("file = %q", got)
	}
	info, err := os.Stat(nested)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(nested))
	if err != nil {
		t.Fatal(err)
	}
	if parent.Mode().Perm() != 0o700 {
		t.Errorf("parent mode = %v, want 0700", parent.Mode().Perm())
	}
}

func TestApplyFilesRejectsPathsOutsideRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"relative":         "app.json",
		"parent traversal": filepath.Join(root, "..", "escape.json"),
		"root itself":      root,
		"sibling prefix":   root + "-sibling.json",
		"outside":          filepath.Join(base, "other", "app.json"),
	}
	for name, path := range paths {
		update := func([]byte) ([]byte, error) { return []byte("x"), nil }
		err := applyFiles(root, []harness.LaunchFile{{Path: path, Update: update}})
		if !errors.Is(err, errFileOutsideRoot) {
			t.Errorf("%s: applyFiles(%q) error = %v, want errFileOutsideRoot", name, path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "escape.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a rejected path wrote outside the root: %v", err)
	}
}

func TestApplyFilesRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "link", "app.json")
	update := func([]byte) ([]byte, error) { return []byte("x"), nil }
	if err := applyFiles(root, []harness.LaunchFile{{Path: path, Update: update}}); err == nil {
		t.Fatal("applyFiles() through a symlink to outside the root succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "app.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file was written through the symlink: %v", err)
	}
}

func TestApplyFilesPropagatesUpdateErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []harness.LaunchFile{{
		Path:   filepath.Join(root, "app.json"),
		Update: func([]byte) ([]byte, error) { return nil, errFixtureUpdate },
	}}
	if err := applyFiles(root, files); !errors.Is(err, errFixtureUpdate) {
		t.Fatalf("applyFiles() error = %v, want the update's error", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed update still wrote the file: %v", err)
	}
}

func TestApplyFilesRequiresUpdate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []harness.LaunchFile{{Path: filepath.Join(root, "app.json")}}
	if err := applyFiles(root, files); !errors.Is(err, errNoFileUpdate) {
		t.Fatalf("applyFiles() error = %v, want errNoFileUpdate", err)
	}
}
