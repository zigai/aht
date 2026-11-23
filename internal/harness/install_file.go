package harness

import (
	"fmt"
	"os"
	"path/filepath"
)

func WriteFileAtomicMode(
	path string,
	data []byte,
	mode os.FileMode,
	createDirError string,
	writeError string,
) error {
	targetPath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		targetPath = resolved
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s: %w", createDirError, err)
	}

	temp, err := os.CreateTemp(dir, filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", targetPath, err)
	}

	tempPath := temp.Name()

	keep := false
	defer func() {
		if keep {
			return
		}

		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(mode); err != nil {
		return fmt.Errorf("setting temp file permissions: %w", err)
	}

	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("%s: %w", writeError, err)
	}

	if err := temp.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("%s: %w", writeError, err)
	}

	keep = true

	return SyncDir(dir)
}

func SyncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening directory: %w", err)
	}
	defer func() {
		_ = handle.Close()
	}()

	if err := handle.Sync(); err != nil {
		return fmt.Errorf("syncing directory: %w", err)
	}

	return nil
}
