package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/strata"
)

// EditPath returns the config file that edits apply to: path when set, then
// $AHT_CONFIG, then the user config file.
func EditPath(path string) (string, error) {
	if path == "-" {
		return "", ErrStdinNotEditable
	}

	options := []strata.Option{strata.WithFormats(".toml")}
	if path != "" {
		options = append(options, strata.WithPath(path))
	} else if env := strings.TrimSpace(os.Getenv(ConfigEnv)); env != "" {
		options = append(options, strata.WithOptionalPath(env))
	} else {
		options = append(options, strata.WithAppName("aht"))
	}

	resolved, err := strata.EditPath(options...)
	if err != nil {
		return "", fmt.Errorf("select config edit path: %w", err)
	}

	return resolved, nil
}

// FileExists reports whether a config file exists at path.
func FileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("%w %s: %w", ErrAccessConfig, path, err)
	}

	if err == nil && info.IsDir() {
		return false, fmt.Errorf("%w: %s", ErrConfigIsDirectory, path)
	}

	return err == nil, nil
}

// Set writes key = value to the config file at path, creating the file from
// the default template when it is missing. A key or value that would leave the
// file invalid returns ErrInvalidSetting and leaves the file unchanged.
func Set(path string, key string, value string) error {
	if _, err := EnsureConfigFile(path); err != nil {
		return fmt.Errorf("ensure config file %s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}

	updated, err := strata.SetBytes(".toml", data, key, value)
	if err != nil {
		return fmt.Errorf("%w: set key %q: %w", ErrInvalidSetting, key, err)
	}

	_, err = strata.Load[Config](
		strata.WithPath("-"),
		strata.WithStdin(bytes.NewReader(updated)),
		strata.WithFormats(".toml"),
		strata.WithStrict(),
	)
	if err != nil {
		return fmt.Errorf("%w: updated configuration is invalid: %w", ErrInvalidSetting, err)
	}

	if err := strata.Set[Config](path, key, value); err != nil {
		return fmt.Errorf("%w: set key %q: %w", ErrInvalidSetting, key, err)
	}

	return nil
}

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
