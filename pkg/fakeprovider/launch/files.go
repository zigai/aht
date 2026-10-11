package launch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

var errFileOutsideRoot = errors.New("launch file must be an absolute path inside the root")

func applyFiles(root string, files []harness.LaunchFile) error {
	if len(files) == 0 {
		return nil
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open launch root: %w", err)
	}
	return errors.Join(writeFiles(dir, root, files), dir.Close())
}

func writeFiles(dir *os.Root, root string, files []harness.LaunchFile) error {
	for _, file := range files {
		if err := writeFile(dir, root, file); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dir *os.Root, root string, file harness.LaunchFile) error {
	name, err := relativeFilePath(root, file.Path)
	if err != nil {
		return err
	}
	if file.Update == nil {
		return fmt.Errorf("%s: %w", file.Path, errNoFileUpdate)
	}
	existing, err := dir.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		existing = nil
	} else if err != nil {
		return fmt.Errorf("read launch file %s: %w", file.Path, err)
	}
	content, err := file.Update(existing)
	if err != nil {
		return fmt.Errorf("update launch file %s: %w", file.Path, err)
	}
	if err := dir.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return fmt.Errorf("create directory for launch file %s: %w", file.Path, err)
	}
	if err := dir.WriteFile(name, content, 0o600); err != nil {
		return fmt.Errorf("write launch file %s: %w", file.Path, err)
	}
	return nil
}

func relativeFilePath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q: %w", path, errFileOutsideRoot)
	}
	name, err := filepath.Rel(root, path)
	if err != nil || name == "." || !filepath.IsLocal(name) {
		return "", fmt.Errorf("%q: %w", path, errFileOutsideRoot)
	}
	return name, nil
}
