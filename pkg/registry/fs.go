package registry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

var registryFSByDir sync.Map

type registryFile interface {
	io.Reader
	io.ReaderAt
	io.Writer
	Close() error
	Chmod(mode os.FileMode) error
	Name() string
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(size int64) error
}

type registryFS interface {
	Open(path string) (registryFile, error)
	OpenFile(path string, flag int, mode os.FileMode) (registryFile, error)
	CreateTemp(dir, pattern string) (registryFile, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
	MkdirAll(path string, mode os.FileMode) error
}

type osRegistryFS struct{}

func (osRegistryFS) Open(path string) (registryFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return file, nil
}

func (osRegistryFS) OpenFile(path string, flag int, mode os.FileMode) (registryFile, error) {
	file, err := os.OpenFile(path, flag, mode)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return file, nil
}

func (osRegistryFS) CreateTemp(dir, pattern string) (registryFile, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("creating temporary store in %s: %w", dir, err)
	}
	return file, nil
}

func (osRegistryFS) Rename(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("renaming %s: %w", oldPath, err)
	}
	return nil
}

func (osRegistryFS) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}

func (osRegistryFS) MkdirAll(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("creating directory %s: %w", path, err)
	}
	return nil
}

func registryFSFor(path string) registryFS {
	if value, ok := registryFSByDir.Load(filepath.Dir(path)); ok {
		fs, valid := value.(registryFS)
		if !valid {
			panic("registry filesystem has invalid type")
		}
		return fs
	}
	return osRegistryFS{}
}

func registryFSForDir(dir string) registryFS {
	if value, ok := registryFSByDir.Load(dir); ok {
		fs, valid := value.(registryFS)
		if !valid {
			panic("registry filesystem has invalid type")
		}
		return fs
	}
	return osRegistryFS{}
}
