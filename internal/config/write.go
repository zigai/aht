package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// publishConfigFile publishes complete, synced contents in one directory entry
// operation. Linking a temporary file gives ensure-only creation O_EXCL semantics
// without exposing a partially written destination to concurrent readers.
func publishConfigFile(path string, overwrite bool) (bool, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirMode); err != nil {
		return false, fmt.Errorf("%w %s: %w", ErrAccessConfig, path, err)
	}

	tmp, err := os.CreateTemp(dir, ".aht-config-*")
	if err != nil {
		return false, fmt.Errorf("create temporary config: %w", err)
	}
	// Cleanup is best effort: failure leaves only a private, unreferenced temp file.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := writeConfigContents(tmp); err != nil {
		return false, err
	}

	if overwrite {
		err = os.Rename(tmp.Name(), path)
	} else {
		err = os.Link(tmp.Name(), path)
	}

	if !overwrite && errors.Is(err, os.ErrExist) {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, fmt.Errorf("%w %s: %w", ErrAccessConfig, path, statErr)
		}

		if info.IsDir() {
			return false, fmt.Errorf("%w: %s", ErrConfigIsDirectory, path)
		}

		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("publish config %s: %w", path, err)
	}

	if err := syncConfigDirectory(dir); err != nil {
		// The complete file is visible even though crash durability is uncertain.
		return true, err
	}

	return true, nil
}

func writeConfigContents(file *os.File) error {
	if err := file.Chmod(defaultFileMode); err != nil {
		return errors.Join(fmt.Errorf("set config permissions: %w", err), file.Close())
	}

	if _, err := file.WriteString(DefaultConfigTemplate()); err != nil {
		return errors.Join(fmt.Errorf("write config: %w", err), file.Close())
	}

	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync config: %w", err), file.Close())
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}

	return nil
}

func syncConfigDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config directory: %w", err)
	}

	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}

	return nil
}
