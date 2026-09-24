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

	"github.com/zigai/aht/internal/harness/catalog"

	"github.com/zigai/aht/pkg/registry"
)

const (
	maxScanWorkers = 16
	minScanWorkers = 2
)

var errSymlinkCycle = errors.New("too many levels of symbolic links")

// DefaultSources resolves native environment overrides and standard history
// locations. Missing directories are normal and do not make search incomplete.
// Unsupported readers remain visible in Result.Sources instead of appearing empty.
func DefaultSources() ([]Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate history home: %w", err)
	}
	var sources []Source
	for _, adapter := range catalog.All() {
		reader := catalog.TranscriptFor(adapter.Definition().ID)
		if reader.Sources != nil {
			for _, path := range reader.Sources(home) {
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
	before := len(s.result.Issues) + s.result.OmittedIssues
	if info.IsDir() {
		s.loadSourceMetadata(source, path, true)
		s.scanDirectory(ctx, source, &status)
	} else {
		resolved, resolveErr := resolveSymlinkFile(path)
		if resolveErr != nil {
			s.issue(source, path, resolveErr)
		} else {
			s.scanFile(ctx, source, resolved, &status)
		}
	}
	if len(s.result.Issues)+s.result.OmittedIssues > before {
		status.Status = "failed"
	}
}

func (s *search) scanDirectory(ctx context.Context, source Source, status *SourceStatus) {
	root, err := os.OpenRoot(source.Path)
	if err != nil {
		s.issue(source, source.Path, err)
		return
	}
	defer s.closeReader(source, source.Path, root)
	var filesToScan []string
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
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
		if !process {
			return nil
		}

		if s.index != nil {
			s.scanEntry(ctx, source, path, root, status)
		} else {
			filesToScan = append(filesToScan, path)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		s.issue(source, source.Path, err)
	}
	if s.index == nil && len(filesToScan) > 0 {
		s.scanFilesParallel(ctx, source, filesToScan, root, status)
	}
}

func (s *search) scanFilesParallel(ctx context.Context, source Source, files []string, root *os.Root, status *SourceStatus) {
	if len(files) == 1 {
		s.scanEntry(ctx, source, files[0], root, status)
		return
	}
	workers := min(maxScanWorkers, len(files), max(minScanWorkers, runtime.GOMAXPROCS(0)))
	var totalFiles atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := range workers {
		go func(workerID int) {
			defer wg.Done()
			for i := workerID; i < len(files); i += workers {
				if ctx.Err() != nil {
					return
				}
				s.scanEntryDirect(ctx, source, files[i], root, &totalFiles)
			}
		}(w)
	}
	wg.Wait()
	status.Files += int(totalFiles.Load())
}

func (s *search) scanEntryDirect(ctx context.Context, source Source, path string, root *os.Root, totalFiles *atomic.Int64) {
	absolute := filepath.Join(source.Path, path)
	if isDatabase(path) {
		s.scanFile(ctx, source, absolute, nil)
		totalFiles.Add(1)
		return
	}
	file, openErr := root.Open(path)
	if openErr != nil {
		s.issue(source, absolute, openErr)
		return
	}
	totalFiles.Add(1)
	s.scanTranscript(ctx, source, absolute, file)
	if err := file.Close(); err != nil {
		s.issue(source, absolute, err)
	}
}

func (s *search) shouldVisitEntry(source Source, path string, entry fs.DirEntry) (bool, bool) {
	if entry.IsDir() {
		return skipHistoryDirectory(source.Harness, path), false
	}
	return false, entry.Type().IsRegular() && historyFile(source.Harness, entry.Name())
}

func (s *search) scanFile(ctx context.Context, source Source, path string, status *SourceStatus) {
	info, err := os.Lstat(path)
	if err != nil {
		s.issue(source, path, err)
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	s.loadSourceMetadata(source, path, false)
	if status != nil {
		status.Files++
	}
	if isDatabase(path) {
		s.scanDatabase(ctx, source, path)
		return
	}
	if s.index != nil && s.index.unchanged(ctx, s, source, path, info) {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		s.issue(source, path, err)
		return
	}
	s.scanTranscript(ctx, source, path, file)
	if err := file.Close(); err != nil {
		s.issue(source, path, err)
	}
}

func isDatabase(path string) bool {
	return strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".sqlite")
}

func historyFile(h registry.Harness, name string) bool {
	for _, pattern := range catalog.TranscriptFor(h).Patterns {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func (s *search) scanEntry(ctx context.Context, source Source, path string, root *os.Root, status *SourceStatus) {
	absolute := filepath.Join(source.Path, path)
	if isDatabase(path) {
		s.scanFile(ctx, source, absolute, status)
		return
	}
	if s.index != nil {
		// Stat through the walk's root to retain its containment guarantees.
		if info, err := root.Stat(path); err == nil && s.index.unchanged(ctx, s, source, absolute, info) {
			status.Files++
			return
		}
	}
	file, openErr := root.Open(path)
	if openErr != nil {
		s.issue(source, absolute, openErr)
		return
	}
	status.Files++
	s.scanTranscript(ctx, source, absolute, file)
	if closeErr := file.Close(); closeErr != nil {
		s.issue(source, absolute, closeErr)
	}
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
		if s.query.Harness != "" || s.explicitSources {
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
