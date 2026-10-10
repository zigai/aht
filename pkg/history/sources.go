package history

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	maxScanWorkers = 16
	minScanWorkers = 2
)

var errSymlinkCycle = errors.New("too many levels of symbolic links")

// historyFile is one native history inside a source. Directory walks open and
// stat through their root to keep its containment guarantees. children are
// histories that belong to this file's conversation, in walk order.
type historyFile struct {
	path     string
	database bool
	open     func() (*os.File, error)
	stat     func() (os.FileInfo, error)
	children []historyFile
}

// DefaultSources resolves native environment overrides and standard history
// locations. Missing directories are normal and do not make search incomplete.
// Unsupported readers remain visible in Result.Sources instead of appearing empty.
func DefaultSources() ([]Source, error) {
	home := harness.HomeDir()
	if home == "" {
		return nil, fmt.Errorf("locate history home: %w", harness.ErrHomeUnknown)
	}
	var sources []Source
	for _, adapter := range catalog.All() {
		reader := catalog.TranscriptFor(adapter.Definition().ID)
		if reader.Sources != nil {
			paths, err := reader.Sources(home)
			if err != nil {
				return nil, fmt.Errorf("discover %s history sources: %w", adapter.Definition().ID, err)
			}
			for _, path := range paths {
				sources = append(sources, Source{Harness: adapter.Definition().ID, Path: path})
			}
		}
	}
	return sources, nil
}

func supported(h registry.Harness) bool { return len(catalog.TranscriptFor(h).Patterns) > 0 }

func (s *search) scanSource(ctx context.Context, source Source) {
	status := SourceStatus{Source: source, Status: "searched", Files: 0}
	defer func() { s.result.Sources = append(s.result.Sources, status) }()
	resolved, info, ok := s.inspectSource(source, &status)
	if !ok {
		return
	}
	source = resolved
	path := source.Path
	before := s.failureCount()
	var files []historyFile
	if info.IsDir() {
		root, err := os.OpenRoot(path)
		if err != nil {
			s.issue(source, path, err)
			status.Status = "failed"
			return
		}
		defer s.closeReader(source, path, root)
		s.loadSourceMetadata(source, path, true)
		files = groupChildren(source.Harness, s.walkDirectory(ctx, source, root))
	} else if file, ok := s.singleFile(source, path); ok {
		s.loadSourceMetadata(source, file.path, false)
		files = []historyFile{file}
	}
	if s.index != nil {
		s.index.scanFiles(ctx, s, source, files, &status)
	} else {
		s.scanFilesParallel(ctx, source, files, &status)
	}
	if s.failureCount() > before {
		status.Status = "failed"
	}
}

func (s *search) failureCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failures
}

func (s *search) walkDirectory(ctx context.Context, source Source, root *os.Root) []historyFile {
	var files []historyFile
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := s.checkScan(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			s.issue(source, filepath.Join(source.Path, path), walkErr)
			return nil
		}
		skip, process := s.shouldVisitEntry(source, path, entry)
		if skip {
			return fs.SkipDir
		}
		if process {
			files = append(files, rootFile(root, source.Path, path))
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		s.issue(source, source.Path, err)
	}
	return files
}

// groupChildren attaches each history whose native parent was also found to
// that parent. A child whose parent is missing stays a history of its own.
func groupChildren(h registry.Harness, files []historyFile) []historyFile {
	parent := catalog.TranscriptFor(h).Parent
	if parent == nil {
		return files
	}
	positions := make(map[string]int, len(files))
	for i, file := range files {
		positions[file.path] = i
	}
	owners := make([]int, len(files))
	for i, file := range files {
		owners[i] = -1
		if owner, ok := positions[parent(file.path)]; ok && owner != i && !file.database {
			owners[i] = owner
		}
	}
	for i, owner := range owners {
		if owner >= 0 {
			files[owner].children = append(files[owner].children, files[i])
		}
	}
	grouped := files[:0]
	for i, file := range files {
		if owners[i] < 0 {
			grouped = append(grouped, file)
		}
	}
	return grouped
}

func rootFile(root *os.Root, base, relative string) historyFile {
	absolute := filepath.Join(base, relative)
	if isDatabase(relative) {
		return historyFile{path: absolute, database: true, open: nil, stat: func() (os.FileInfo, error) { return os.Stat(absolute) }, children: nil}
	}
	return historyFile{
		path:     absolute,
		database: false,
		open:     func() (*os.File, error) { return root.Open(relative) },
		stat:     func() (os.FileInfo, error) { return root.Stat(relative) },
		children: nil,
	}
}

// singleFile resolves an explicit file source. Non-regular files are skipped.
func (s *search) singleFile(source Source, path string) (historyFile, bool) {
	resolved, err := resolveSymlinkFile(path)
	if err != nil {
		s.issue(source, path, err)
		return historyFile{path: "", database: false, open: nil, stat: nil, children: nil}, false
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		s.issue(source, resolved, err)
		return historyFile{path: "", database: false, open: nil, stat: nil, children: nil}, false
	}
	if !info.Mode().IsRegular() {
		return historyFile{path: "", database: false, open: nil, stat: nil, children: nil}, false
	}
	file := historyFile{
		path:     resolved,
		database: isDatabase(resolved),
		open:     func() (*os.File, error) { return os.Open(resolved) },
		stat:     func() (os.FileInfo, error) { return os.Stat(resolved) },
		children: nil,
	}
	if file.database {
		file.open = nil
	}
	return file, true
}

// scanFilesParallel scans without the index. Matches are ranked afterwards, so
// workers may finish files in any order.
func (s *search) scanFilesParallel(ctx context.Context, source Source, files []historyFile, status *SourceStatus) {
	if len(files) == 0 {
		return
	}
	workers := min(maxScanWorkers, len(files), max(minScanWorkers, runtime.GOMAXPROCS(0)))
	var next, done, inspected atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) || ctx.Err() != nil || s.halted() {
					return
				}
				inspected.Add(int64(s.scanFileDirect(ctx, source, files[i])))
				s.report(Progress{Source: source, Done: int(done.Add(1)), Total: len(files), Refreshed: 0})
			}
		}()
	}
	wg.Wait()
	status.Files += int(inspected.Load())
}

// scanFileDirect parses one history with its children and returns how many
// files it inspected.
func (s *search) scanFileDirect(ctx context.Context, source Source, file historyFile) int {
	if file.database {
		s.scanDatabase(ctx, source, file.path)
		return 1
	}
	inspected := 0
	t, ok := s.readTranscriptFile(ctx, source, file)
	if ok {
		inspected++
	}
	for _, child := range file.children {
		c, ok := s.readTranscriptFile(ctx, source, child)
		if !ok {
			continue
		}
		inspected++
		switch {
		case c == nil:
		case t != nil && c.match.Conversation.SessionID == t.match.Conversation.SessionID:
			t.absorb(c, s.query.Excerpts)
		default:
			s.add(ctx, c)
		}
	}
	if t != nil {
		s.add(ctx, t)
	}
	return inspected
}

func (s *search) readTranscriptFile(ctx context.Context, source Source, file historyFile) (*transcript, bool) {
	opened, err := file.open()
	if err != nil {
		s.issue(source, file.path, err)
		return nil, false
	}
	t := s.scanTranscript(ctx, source, file.path, opened)
	if err := opened.Close(); err != nil {
		s.issue(source, file.path, err)
	}
	return t, true
}

func (s *search) report(progress Progress) {
	if s.progress == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress(progress)
}

func (s *search) shouldVisitEntry(source Source, path string, entry fs.DirEntry) (bool, bool) {
	if entry.IsDir() {
		return skipHistoryDirectory(source.Harness, path), false
	}
	return false, entry.Type().IsRegular() && historyName(source.Harness, entry.Name())
}

func isDatabase(path string) bool {
	return strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".sqlite")
}

func historyName(h registry.Harness, name string) bool {
	for _, pattern := range catalog.TranscriptFor(h).Patterns {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func isIgnoredDirName(name string) bool {
	switch name {
	case ".git", "node_modules", ".cache", ".npm", ".cargo", "vendor", "__pycache__", ".venv", "venv":
		return true
	default:
		return false
	}
}

func skipHistoryDirectory(h registry.Harness, path string) bool {
	if path == "." {
		return false
	}
	if isIgnoredDirName(filepath.Base(path)) {
		return true
	}
	if skip := catalog.TranscriptFor(h).SkipDirectory; skip != nil {
		return skip(path)
	}
	return false
}

// inspectSource resolves and stats a source before checking reader support, so an
// absent path reports missing rather than unsupported. Only an existing path
// without a verified reader is unsupported, and only then does it become an issue
// when the caller selected it explicitly or filtered by harness.
func (s *search) inspectSource(source Source, status *SourceStatus) (Source, fs.FileInfo, bool) {
	if source.Path == "" {
		status.Status = "failed"
		s.issue(source, "", ErrInvalidSource)
		return source, nil, false
	}
	path, err := filepath.Abs(source.Path)
	if err != nil {
		s.issue(source, source.Path, err)
		status.Status = "failed"
		return source, nil, false
	}
	source.Path = path
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		status.Status = "missing"
		if s.explicitSources {
			s.issue(source, path, err)
		}
		return source, nil, false
	}
	if err != nil {
		s.issue(source, path, err)
		status.Status = "failed"
		return source, nil, false
	}
	if !supported(source.Harness) {
		status.Status = "unsupported"
		if len(s.query.Harnesses) > 0 || s.explicitSources {
			s.issue(source, path, ErrUnsupportedHarness)
		}
		return source, nil, false
	}

	return source, info, true
}

func (s *search) closeReader(source Source, path string, reader io.Closer) {
	if err := reader.Close(); err != nil {
		s.issue(source, path, err)
	}
}

func (s *search) checkScan(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("walk history: %w", err)
	}

	return nil
}

// resolveSymlinkFile resolves symlinks for a single file source while preserving
// the lexical directory path of non-symlink parents.
func resolveSymlinkFile(path string) (string, error) {
	for range 64 {
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("resolve symlink source: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read symlink target: %w", err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return "", errSymlinkCycle
}

func (s *search) loadSourceMetadata(source Source, path string, isDir bool) {
	s.sourceMetadata = nil
	if load := catalog.TranscriptFor(source.Harness).SourceMetadata; load != nil {
		s.sourceMetadata = load(path, isDir, func(path string, err error) { s.issue(source, path, err) })
	}
}
