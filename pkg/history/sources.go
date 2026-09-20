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

	"github.com/zigai/aht/pkg/registry"
)

// Native history basenames, including retired OpenClaw transcript artifacts.
var sourcePatterns = map[registry.Harness][]string{
	registry.HarnessClaude:   {"*.jsonl"},
	registry.HarnessCodex:    {"rollout-*.jsonl"},
	registry.HarnessPi:       {"*.jsonl"},
	registry.HarnessOmp:      {"*.jsonl"},
	registry.HarnessCopilot:  {"events.jsonl"},
	registry.HarnessKimiCode: {"context.jsonl"},
	registry.HarnessCline:    {"*.messages.json"},
	registry.HarnessOpenCode: {"opencode*.db"},
	registry.HarnessKilo:     {"kilo*.db", "opencode*.db"},
	registry.HarnessGoose:    {"sessions.db"},
	registry.HarnessGrok:     {"grok.db"},
	registry.HarnessHermes:   {"state.db"},
	registry.HarnessOpenClaw: {"openclaw-agent.sqlite", "*.jsonl", "*.jsonl.deleted.*", "*.jsonl.reset.*"},
	registry.HarnessCursor:   nil,
	registry.HarnessAgy:      nil,
	registry.HarnessDroid:    nil,
}

// DefaultSources resolves native environment overrides and standard history
// locations. Missing directories are normal and do not make search incomplete.
// Unsupported readers remain visible in Result.Sources instead of appearing empty.
func DefaultSources() ([]Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locate history home: %w", err)
	}
	data := envPath("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	codex := envPath("CODEX_HOME", filepath.Join(home, ".codex"))
	goose := filepath.Join(data, "goose")
	if runtime.GOOS == "darwin" {
		goose = filepath.Join(home, "Library", "Application Support", "Block", "goose")
	}
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		goose = filepath.Join(root, "data")
	}
	opencode := databaseLocation(filepath.Join(data, "opencode"), "OPENCODE_DB")
	kilo := databaseLocation(filepath.Join(data, "kilo"), "KILO_DB")

	return []Source{
		{Harness: registry.HarnessClaude, Path: filepath.Join(envPath("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "projects")},
		{Harness: registry.HarnessCodex, Path: filepath.Join(codex, "sessions")},
		{Harness: registry.HarnessCodex, Path: filepath.Join(codex, "archived_sessions")},
		{Harness: registry.HarnessPi, Path: filepath.Join(home, ".pi", "agent", "sessions")},
		{Harness: registry.HarnessOmp, Path: filepath.Join(home, ".omp", "agent", "sessions")},
		{Harness: registry.HarnessCopilot, Path: filepath.Join(home, ".copilot", "session-state")},
		{Harness: registry.HarnessKimiCode, Path: filepath.Join(envPath("KIMI_SHARE_DIR", filepath.Join(home, ".kimi")), "sessions")},
		{Harness: registry.HarnessOpenCode, Path: opencode},
		{Harness: registry.HarnessKilo, Path: kilo},
		{Harness: registry.HarnessGoose, Path: filepath.Join(goose, "sessions", "sessions.db")},
		{Harness: registry.HarnessGrok, Path: filepath.Join(home, ".grok", "grok.db")},
		{Harness: registry.HarnessHermes, Path: filepath.Join(envPath("HERMES_HOME", filepath.Join(home, ".hermes")), "state.db")},
		{Harness: registry.HarnessOpenClaw, Path: filepath.Join(envPath("OPENCLAW_STATE_DIR", filepath.Join(home, ".openclaw")), "agents")},
		{Harness: registry.HarnessCline, Path: envPath("CLINE_SESSION_DATA_DIR", filepath.Join(envPath("CLINE_DATA_DIR", filepath.Join(home, ".cline", "data")), "sessions"))},
		{Harness: registry.HarnessCursor, Path: filepath.Join(home, ".cursor")},
		{Harness: registry.HarnessAgy, Path: filepath.Join(home, ".gemini", "antigravity-cli")},
		{Harness: registry.HarnessDroid, Path: filepath.Join(home, ".factory", "sessions")},
	}, nil
}

func envPath(name, fallback string) string {
	if path := os.Getenv(name); path != "" {
		return path
	}
	return fallback
}

func supported(h registry.Harness) bool { return len(sourcePatterns[h]) > 0 }

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
		if source.Harness == registry.HarnessKimiCode {
			s.kimiDirs = s.kimiMetadata(source, path)
		}
		s.scanDirectory(ctx, source, &status)
	} else {
		resolved, resolveErr := filepath.EvalSymlinks(path)
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
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := s.checkScan(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			s.issue(source, filepath.Join(source.Path, path), walkErr)
			return nil
		}
		if entry.IsDir() {
			if skipHistoryDirectory(source.Harness, path) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !historyFile(source.Harness, entry.Name()) {
			return nil
		}

		s.scanEntry(ctx, source, path, root, status)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		s.issue(source, source.Path, err)
	}
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
	if source.Harness == registry.HarnessKimiCode {
		s.kimiDirs = nil
		// Native files live at sessions/<work-dir-key>/<session-id>/context.jsonl.
		// The caller resolves explicitly selected symlinks before reaching here.
		sessionsDir := filepath.Dir(filepath.Dir(filepath.Dir(path)))
		if filepath.Base(sessionsDir) == "sessions" {
			s.kimiDirs = s.kimiMetadata(source, sessionsDir)
		}
	}
	status.Files++
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
	for _, pattern := range sourcePatterns[h] {
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

func skipHistoryDirectory(h registry.Harness, path string) bool {
	if path == "." {
		return false
	}
	if h == registry.HarnessOpenCode || h == registry.HarnessKilo {
		return true
	}
	if h != registry.HarnessOpenClaw {
		return false
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) <= 1 {
		return false
	}
	return parts[1] != "agent" && parts[1] != "sessions"
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

func databaseLocation(root, variable string) string {
	value := os.Getenv(variable)
	if value == "" || value == ":memory:" {
		return root
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, value)
}
