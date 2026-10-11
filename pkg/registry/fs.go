package registry

import (
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
	Lstat(path string) (os.FileInfo, error)
}

type osRegistryFS struct{}

func (osRegistryFS) Open(path string) (registryFile, error) {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.Open(path)
}

func (osRegistryFS) OpenFile(path string, flag int, mode os.FileMode) (registryFile, error) {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.OpenFile(path, flag, mode)
}

func (osRegistryFS) CreateTemp(dir, pattern string) (registryFile, error) {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.CreateTemp(dir, pattern)
}

func (osRegistryFS) Rename(oldPath, newPath string) error {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.Rename(oldPath, newPath)
}

func (osRegistryFS) Remove(path string) error {
	//nolint:wrapcheck // Preserve os.PathError text on best-effort cleanup.
	return os.Remove(path)
}

func (osRegistryFS) MkdirAll(path string, mode os.FileMode) error {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.MkdirAll(path, mode)
}

func (osRegistryFS) Lstat(path string) (os.FileInfo, error) {
	//nolint:wrapcheck // Preserve os.PathError text; the caller adds context.
	return os.Lstat(path)
}

func registryFSFor(path string) registryFS {
	return registryFSForDir(filepath.Dir(path))
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
