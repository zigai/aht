package history

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/unix"
)

type indexedDirectory struct {
	stamp    string
	children []string
	files    []string
}

type directoryCache struct {
	source   Source
	previous map[string]indexedDirectory
	current  map[string]indexedDirectory
	files    map[string][]string
}

type directoryEntry struct {
	name      string
	directory bool
}

type directoryChange struct {
	source    Source
	path      string
	directory *indexedDirectory
}

func (index *historyIndex) loadDirectories(ctx context.Context, source Source) (_ *directoryCache, err error) {
	cache := &directoryCache{source: source, previous: map[string]indexedDirectory{}, current: map[string]indexedDirectory{}, files: map[string][]string{}}
	rows, err := index.conn.QueryContext(ctx, "SELECT path,stamp,children FROM directories WHERE harness=? AND root=?", source.Harness, source.Path)
	if err != nil {
		return nil, fmt.Errorf("read indexed directories: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var path, children string
		var directory indexedDirectory
		if err := rows.Scan(&path, &directory.stamp, &children); err != nil {
			return nil, fmt.Errorf("read indexed directory: %w", err)
		}
		if err := json.Unmarshal([]byte(children), &directory.children); err != nil {
			return nil, fmt.Errorf("decode indexed child directories: %w", err)
		}
		cache.previous[path] = directory
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read indexed directories: %w", err)
	}
	for _, file := range index.files {
		if file.harness != string(source.Harness) {
			continue
		}
		relative, err := filepath.Rel(source.Path, file.path)
		if err == nil && filepath.IsLocal(relative) && relative != "." {
			parent := filepath.Dir(relative)
			cache.files[parent] = append(cache.files[parent], filepath.Base(relative))
		}
	}
	index.directories = append(index.directories, cache)
	return cache, nil
}

func (index *historyIndex) walkDirectory(ctx context.Context, search *search, source Source, root *os.Root) []historyFile {
	cache, err := index.loadDirectories(ctx, source)
	if err != nil {
		index.report(search, source, source.Path, err)
		return nil
	}
	var files []historyFile
	cache.walk(ctx, search, root, ".", &files)
	return files
}

func (cache *directoryCache) walk(ctx context.Context, search *search, root *os.Root, path string, files *[]historyFile) {
	if search.checkScan(ctx) != nil {
		return
	}
	info, err := root.Lstat(path)
	if err != nil {
		search.issue(cache.source, filepath.Join(cache.source.Path, path), err)
		return
	}
	if !info.IsDir() {
		return
	}
	directory, err := cache.read(search, root, path, info)
	if err != nil {
		search.issue(cache.source, filepath.Join(cache.source.Path, path), err)
		return
	}
	cache.current[path] = directory
	entries := make([]directoryEntry, 0, len(directory.children)+len(directory.files))
	for _, name := range directory.children {
		entries = append(entries, directoryEntry{name: name, directory: true})
	}
	for _, name := range directory.files {
		entries = append(entries, directoryEntry{name: name, directory: false})
	}
	slices.SortFunc(entries, func(left, right directoryEntry) int { return cmp.Compare(left.name, right.name) })
	for _, entry := range entries {
		if search.checkScan(ctx) != nil {
			return
		}
		child := filepath.Join(path, entry.name)
		if entry.directory {
			cache.walk(ctx, search, root, child, files)
		} else {
			*files = append(*files, rootFile(root, cache.source.Path, child))
		}
	}
}

func (cache *directoryCache) read(search *search, root *os.Root, path string, info os.FileInfo) (indexedDirectory, error) {
	stamp := stampFile(info)
	if info.ModTime().Nanosecond() == 0 {
		stamp = ""
	}
	if previous, ok := cache.previous[path]; ok && stamp != "" && previous.stamp == stamp {
		if unix.Faccessat(unix.AT_FDCWD, filepath.Join(cache.source.Path, path), unix.R_OK, unix.AT_EACCESS) == nil {
			previous.files = cache.files[path]
			return previous, nil
		}
	}
	entries, err := fs.ReadDir(root.FS(), path)
	if err != nil {
		return indexedDirectory{}, fmt.Errorf("read history directory: %w", err)
	}
	directory := indexedDirectory{stamp: stamp, children: nil, files: nil}
	for _, entry := range entries {
		skip, process := search.shouldVisitEntry(cache.source, filepath.Join(path, entry.Name()), entry)
		if entry.IsDir() && !skip {
			directory.children = append(directory.children, entry.Name())
		} else if process {
			directory.files = append(directory.files, entry.Name())
		}
	}
	return directory, nil
}

func (cache *directoryCache) changes(index *historyIndex) []directoryChange {
	var changes []directoryChange
	for path, directory := range cache.current {
		previous, exists := cache.previous[path]
		if !cache.complete(index, path, directory.files) {
			if exists {
				changes = append(changes, directoryChange{source: cache.source, path: path, directory: nil})
			}
			continue
		}
		if !exists || directory.stamp != previous.stamp || !slices.Equal(directory.children, previous.children) {
			changes = append(changes, directoryChange{source: cache.source, path: path, directory: &directory})
		}
	}
	for path := range cache.previous {
		if cache.vanished(path) {
			changes = append(changes, directoryChange{source: cache.source, path: path, directory: nil})
		}
	}
	return changes
}

func (cache *directoryCache) complete(index *historyIndex, path string, files []string) bool {
	for _, name := range files {
		if _, ok := index.files[fileKey(cache.source, filepath.Join(cache.source.Path, path, name))]; !ok {
			return false
		}
	}
	return true
}

func (cache *directoryCache) vanished(path string) bool {
	if _, ok := cache.current[path]; ok {
		return false
	}
	info, err := os.Stat(filepath.Join(cache.source.Path, path))
	return errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir())
}

func (index *historyIndex) saveDirectories(ctx context.Context) error {
	changes := make([]directoryChange, 0, len(index.directories))
	for _, cache := range index.directories {
		changes = append(changes, cache.changes(index)...)
	}
	if len(changes) == 0 {
		return nil
	}
	if err := index.beginWrite(ctx); err != nil {
		return err
	}
	for _, change := range changes {
		if err := index.writeDirectory(ctx, change); err != nil {
			return errors.Join(err, index.rollbackWrite(ctx))
		}
	}
	return index.endWrite(ctx)
}

func (index *historyIndex) writeDirectory(ctx context.Context, change directoryChange) error {
	if change.directory == nil {
		if _, err := index.tx.ExecContext(ctx, "DELETE FROM directories WHERE harness=? AND root=? AND path=?", change.source.Harness, change.source.Path, change.path); err != nil {
			return index.failure(fmt.Errorf("remove indexed directory: %w", err))
		}
		return nil
	}
	children, err := json.Marshal(change.directory.children)
	if err != nil {
		return fmt.Errorf("encode indexed child directories: %w", err)
	}
	if _, err := index.tx.ExecContext(ctx, "INSERT INTO directories(harness,root,path,stamp,children) VALUES(?,?,?,?,?) ON CONFLICT(harness,root,path) DO UPDATE SET stamp=excluded.stamp,children=excluded.children", change.source.Harness, change.source.Path, change.path, change.directory.stamp, string(children)); err != nil {
		return index.failure(fmt.Errorf("save indexed directory: %w", err))
	}
	return nil
}
