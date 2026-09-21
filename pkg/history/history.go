package history

import (
	"bytes"
	"cmp"
	"container/heap"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zigai/aht/internal/pathmatch"
	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	maxExcerpts    = 3
	maxQueryBytes  = 4096
	maxRecordBytes = 16 << 20
	maxIssues      = 100
)

var (
	// ErrInvalidQuery indicates invalid search options.
	ErrInvalidQuery = errors.New("invalid history query")
	// ErrIncomplete means some history could not be searched. The result still
	// contains matches and structured diagnostics; callers must inspect both.
	ErrIncomplete = errors.New("history search incomplete")
	// ErrUnsupportedHarness indicates there is no verified local history reader.
	ErrUnsupportedHarness = errors.New("local history reader unavailable for this harness")
	// ErrInvalidSource indicates an empty source location.
	ErrInvalidSource = errors.New("history source path must not be empty")
	errRecordSize    = errors.New("history record exceeds 16 MiB")
	errUnknownFormat = errors.New("unrecognized history format or missing native session identity")
)

// Source identifies a harness's transcript directory or native SQLite database.
// Explicit sources replace default discovery, allowing custom profiles and exports.
type Source struct {
	Harness registry.Harness `json:"harness"`
	Path    string           `json:"path"`
}

// Query searches literal text, case-insensitively unless CaseSensitive is set.
// Dir matches recorded working directories or workspace roots and their descendants.
// Empty text is invalid. Limit zero returns all matches ordered by most recently
// updated conversation; positive values keep the most recently updated matching
// conversations and set Result.Truncated when more conversations matched.
// Registry is an optional snapshot used only for live-state enrichment; its
// absence means unknown.
type Query struct {
	Text            string             `json:"text"`
	Harness         registry.Harness   `json:"harness,omitempty"`
	Dir             string             `json:"dir,omitempty"`
	Role            string             `json:"role,omitempty"`
	IncludeTools    bool               `json:"include_tools"`
	CaseSensitive   bool               `json:"case_sensitive"`
	Limit           int                `json:"limit"`
	Registry        []registry.Session `json:"-"`
	IgnoreHarnesses []registry.Harness `json:"-"`
	IgnorePaths     []string           `json:"-"`
}

// Catalog reads Sources, or default local sources when Sources is nil.
// A non-nil empty Sources slice searches nothing. IndexPath selects the disposable
// SQLite index; empty uses aht/history-v1.sqlite under [os.UserCacheDir].
type Catalog struct {
	Sources   []Source
	IndexPath string
}

// Conversation identifies native history, including histories never seen by AHT.
// Path is the transcript or database, not an AHT registry path. Unknown metadata
// is omitted; timestamps are native timestamps, never inferred from file mtimes.
type Conversation struct {
	Harness     registry.Harness `json:"harness"`
	SessionID   string           `json:"session_id"`
	Path        string           `json:"path"`
	Title       string           `json:"title,omitempty"`
	CWD         string           `json:"cwd,omitempty"`
	ProjectRoot string           `json:"project_root,omitempty"`
	CreatedAt   time.Time        `json:"created_at,omitzero"`
	UpdatedAt   time.Time        `json:"updated_at,omitzero"`
}

// Excerpt is a bounded window around the first literal match in a message.
// Line is a one-based JSONL line, or zero for databases. MessageID is native when
// available. Role is user, assistant, or tool. Reasoning and system text are excluded.
type Excerpt struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	MessageID string    `json:"message_id,omitempty"`
	Line      int       `json:"line,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}

// LiveState is evidence from the caller's registry snapshot, not a fresh process
// probe. Several tracked incarnations may refer to one historical conversation.
type LiveState struct {
	RegistryID string            `json:"registry_id"`
	Presence   registry.Presence `json:"presence"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Match groups up to three matching message excerpts from one native history.
// MatchingParts counts matching message text segments in that history. No Live entries
// means presence is unknown, not that the conversation has terminated.
type Match struct {
	Conversation  Conversation `json:"conversation"`
	Excerpts      []Excerpt    `json:"excerpts"`
	MatchingParts int          `json:"matching_parts"`
	Live          []LiveState  `json:"live"`
}

// SourceStatus reports a source's coverage: searched, skipped, missing,
// unsupported, or failed. Files counts inspected transcript files/databases.
type SourceStatus struct {
	Source Source `json:"source"`
	Status string `json:"status"`
	Files  int    `json:"files"`
}

// Issue identifies an unreadable, invalid, or bounded source. It never contains
// transcript content. Diagnostics are bounded; additional issues are counted.
type Issue struct {
	Source  Source `json:"source"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Result includes partial matches even when Search returns ErrIncomplete.
// Truncated means more conversations matched than Limit allowed; Matches then
// holds the most recently updated ones. Sources distinguishes skipped, missing,
// and unsupported histories from searched histories.
type Result struct {
	Matches       []Match        `json:"matches"`
	Sources       []SourceStatus `json:"sources"`
	Issues        []Issue        `json:"issues"`
	OmittedIssues int            `json:"omitted_issues"`
	Truncated     bool           `json:"truncated"`
}

type search struct {
	mu              sync.Mutex
	index           *historyIndex
	writer          *indexWriter
	indexErr        error
	explicitSources bool
	kimiDirs        map[string]string
	query           Query
	needle          string
	needleASCII     bool
	needleEscaped   bool
	needleBytes     []byte
	matched         int
	recent          matchHeap
	result          Result
}

type retainedMatch struct {
	Match

	indexID int64
}

// matchHeap holds the newest Limit matches. The root is the least preferred
// match under the result ordering, so a full heap drops it in O(log limit) and
// memory stays O(limit) however many conversations match.
type matchHeap []retainedMatch

func (h *matchHeap) Len() int { return len(*h) }

// Less orders the root at the least preferred match: [heap.Pop] yields the match
// a result limit would drop first.
func (h *matchHeap) Less(i, j int) bool { return compareMatches((*h)[j].Match, (*h)[i].Match) < 0 }

func (h *matchHeap) Swap(i, j int) { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }

func (h *matchHeap) Push(value any) {
	match, ok := value.(retainedMatch)
	if !ok {
		panic("history: matchHeap holds only retainedMatch values")
	}
	*h = append(*h, match)
}

func (h *matchHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

// Validate checks the public query contract without reading any history.
func (q Query) Validate() error {
	if strings.TrimSpace(q.Text) == "" {
		return fmt.Errorf("%w: provide nonempty text", ErrInvalidQuery)
	}
	if len(q.Text) > maxQueryBytes {
		return fmt.Errorf("%w: text exceeds 4096 bytes", ErrInvalidQuery)
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: limit must be nonnegative (0 means unlimited)", ErrInvalidQuery)
	}
	if q.Harness != "" {
		if _, err := harness.Parse(string(q.Harness)); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
		}
	}
	if q.Role != "" && q.Role != "user" && q.Role != "assistant" && q.Role != "agent" && q.Role != "tool" && q.Role != "all" {
		return fmt.Errorf("%w: invalid role %q; choose user, agent, assistant, tool, or all", ErrInvalidQuery, q.Role)
	}
	return nil
}

// Search searches the machine's default local history sources.
func Search(ctx context.Context, query Query) (Result, error) {
	var catalog Catalog
	return catalog.Search(ctx, query)
}

// Search refreshes changed histories and searches the local index, falling back
// to a direct scan when the disposable index cannot be used. Sources and files
// retain discovery and lexical ordering, with bounded native record I/O. Matches
// are ordered by most recently updated conversation. Cancellation returns the
// partial result and the context error. Each call is independent; simultaneous
// calls are safe if callers do not mutate input slices.
func (c Catalog) Search(ctx context.Context, query Query) (Result, error) {
	var s search
	sources, err := c.begin(ctx, query, &s)
	if err != nil {
		return s.finalize(), err
	}
	if len(sources) == 0 {
		return s.finalize(), nil
	}
	err = c.searchIndexed(ctx, &s, sources)
	return s.finalize(), err
}

// searchDirect scans the sources without the disposable index. Both the fallback
// and the indexed path share this exact code, so it is the reference behavior
// the index must reproduce.
func (c Catalog) searchDirect(ctx context.Context, query Query) (Result, error) {
	var s search
	sources, err := c.begin(ctx, query, &s)
	if err != nil {
		return s.finalize(), err
	}
	if len(sources) == 0 {
		return s.finalize(), nil
	}
	err = s.runDirect(ctx, sources)
	return s.finalize(), err
}

// searchIndexed searches through the disposable index and falls back to the
// direct scan when the index is unavailable, either at open time or while
// refreshing a source. A failed attempt is discarded before the fallback so
// partially indexed matches cannot be reported twice.
func (c Catalog) searchIndexed(ctx context.Context, s *search, sources []Source) error {
	index, err := openHistoryIndex(ctx, c.IndexPath, s.indexSources(sources))
	if err != nil {
		if !errors.Is(err, errIndexUnavailable) {
			return err
		}
		return s.runDirect(ctx, sources)
	}
	s.index = index
	runErr := s.runIndexed(ctx, sources)
	closeErr := index.close()
	s.index = nil
	if errors.Is(runErr, errIndexUnavailable) {
		// The index is unusable, so its own close error is irrelevant.
		s.reset()
		return s.runDirect(ctx, sources)
	}
	return errors.Join(runErr, closeErr)
}

// begin validates the query, resolves the sources to scan, and clears the result
// both paths record into. Sources are deduplicated so repeating one cannot
// duplicate its conversations.
func (c Catalog) begin(ctx context.Context, query Query, s *search) ([]Source, error) {
	s.query = query
	s.explicitSources = c.Sources != nil
	s.reset()
	if err := s.prepare(); err != nil {
		return nil, err
	}
	sources := c.Sources
	if sources == nil {
		var err error
		sources, err = DefaultSources()
		if err != nil {
			return nil, err
		}
	}
	sources = dedupeSources(sources)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("search history: %w", err)
	}
	return sources, nil
}

func (s *search) runIndexed(ctx context.Context, sources []Source) error {
	scanErr := s.forEachSource(ctx, sources)
	var commitErr error
	if scanErr == nil {
		commitErr = s.index.commit(ctx)
	}
	excerptErr := s.index.readRetained(ctx, s)
	return errors.Join(scanErr, s.indexErr, commitErr, excerptErr, s.resultError())
}

// indexSources mirrors native discovery's path spelling: directory walks keep
// their selected root, whereas explicit files resolve symlinks before indexing.
func (s *search) indexSources(sources []Source) []Source {
	selected := make([]Source, 0, len(sources))
	for _, source := range sources {
		if _, ok := s.selectSource(source); !ok || source.Path == "" {
			continue
		}
		selected = append(selected, source)
		if info, err := os.Stat(source.Path); err == nil && !info.IsDir() {
			if resolved, err := resolveSymlinkFile(source.Path); err == nil && resolved != source.Path {
				selected = append(selected, Source{Harness: source.Harness, Path: resolved})
			}
		}
	}
	return selected
}

func (s *search) runDirect(ctx context.Context, sources []Source) error {
	if err := s.forEachSource(ctx, sources); err != nil {
		return err
	}
	return s.resultError()
}

// forEachSource scans every selected source in input order. Sources the query
// does not select are reported as skipped coverage; when no source at all was
// selected, each skip also becomes an issue so the search reports incomplete
// coverage instead of silently succeeding with nothing searched.
func (s *search) forEachSource(ctx context.Context, sources []Source) error {
	skipped := make([]Issue, 0, len(sources))
	selected := 0
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("search history: %w", err)
		}
		reason, ok := s.selectSource(source)
		if ok {
			selected++
			s.scanSource(ctx, source)
			continue
		}
		s.result.Sources = append(s.result.Sources, SourceStatus{Source: source, Status: "skipped", Files: 0})
		skipped = append(skipped, Issue{Source: source, Path: source.Path, Message: reason})
	}
	if selected == 0 && len(sources) > 0 {
		for _, issue := range skipped {
			s.recordIssue(issue)
		}
	}
	return nil
}

func (s *search) prepare() error {
	if err := s.query.Validate(); err != nil {
		return err
	}
	if s.query.Harness != "" {
		s.query.Harness, _ = harness.Parse(string(s.query.Harness))
	}
	if s.query.Dir != "" {
		path, err := filepath.Abs(s.query.Dir)
		if err != nil {
			return fmt.Errorf("search directory: %w", err)
		}
		s.query.Dir = path
	}
	s.needle = s.query.Text
	if !s.query.CaseSensitive {
		s.needle = fold(s.needle)
	}
	s.needleBytes = []byte(s.needle)
	s.needleASCII = isASCII(s.needle)
	s.needleEscaped = hasJSONEscapes(s.needle)
	if s.query.Role == "agent" {
		s.query.Role = "assistant"
	}
	if s.query.Role == "all" {
		s.query.Role = ""
	}
	return nil
}

func hasJSONEscapes(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] == '"' || s[i] == '\\' {
			return true
		}
	}
	return false
}

func (s *search) containsNeedle(data []byte) bool {
	if s.needleEscaped {
		return true
	}
	if s.query.CaseSensitive {
		return bytes.Contains(data, s.needleBytes)
	}
	if s.needleASCII && containsFoldASCII(data, s.needle) {
		return true
	}
	if hasNonASCIIBytes(data) {
		return strings.Contains(fold(string(data)), s.needle)
	}
	return false
}

func hasNonASCIIBytes(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// reset clears the result state so a discarded attempt cannot leak matches,
// coverage, or diagnostics into the next one.
func (s *search) reset() {
	s.matched = 0
	s.recent = nil
	s.indexErr = nil
	s.result.Matches = []Match{}
	s.result.Sources = []SourceStatus{}
	s.result.Issues = []Issue{}
	s.result.OmittedIssues = 0
	s.result.Truncated = false
}

// selectSource reports whether the source is selected and, when it is not, the
// reason recorded in the skipped coverage and diagnostics.
func (s *search) selectSource(source Source) (string, bool) {
	if s.query.Harness != "" {
		if source.Harness != s.query.Harness {
			return fmt.Sprintf("source skipped: harness %q does not match the requested harness %q", source.Harness, s.query.Harness), false
		}
		return "", true
	}
	if slices.Contains(s.query.IgnoreHarnesses, source.Harness) {
		return fmt.Sprintf("source skipped: harness %q is excluded by the ignore list", source.Harness), false
	}
	return "", true
}

func (s *search) issue(source Source, path string, err error) {
	s.recordIssue(Issue{Source: source, Path: path, Message: err.Error()})
}

func (s *search) recordIssue(issue Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.result.Issues) >= maxIssues {
		s.result.OmittedIssues++
		return
	}
	s.result.Issues = append(s.result.Issues, issue)
}

func within(path, parent string) bool {
	if path == "" || parent == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *search) accepts(c Conversation) bool {
	if s.query.Dir != "" && !within(c.CWD, s.query.Dir) && !within(c.ProjectRoot, s.query.Dir) {
		return false
	}
	for _, path := range s.query.IgnorePaths {
		if pathmatch.Match(c.CWD, path) || pathmatch.Match(c.ProjectRoot, path) {
			return false
		}
	}
	return true
}

// add accepts a matching conversation into the result. A positive limit keeps
// only the newest matches but never stops the scan: the heap holds the best
// Limit matches seen so far, and Truncated records that more matched.
func (s *search) add(ctx context.Context, m Match) {
	if s.writer != nil {
		s.writer.finish(ctx, m.Conversation)
		return
	}
	s.keep(m, 0)
}

// keep ranks metadata before indexed excerpts are loaded. Direct scans pass a
// zero index ID because they already have their excerpts.
func (s *search) keep(m Match, indexID int64) {
	if m.Conversation.SessionID == "" || m.MatchingParts == 0 || !s.accepts(m.Conversation) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.matched++
	if s.query.Limit == 0 {
		s.result.Matches = append(s.result.Matches, m)
		return
	}
	if len(s.recent) < s.query.Limit {
		heap.Push(&s.recent, retainedMatch{Match: m, indexID: indexID})
	} else if compareMatches(m, s.recent[0].Match) < 0 {
		s.recent[0] = retainedMatch{Match: m, indexID: indexID}
		heap.Fix(&s.recent, 0)
	}
	if s.matched > s.query.Limit {
		s.result.Truncated = true
	}
}

// finalize orders the accepted matches and returns the result. Call it once per
// search on every path that returns a result; Matches is always non-nil.
func (s *search) finalize() Result {
	var ordered []Match
	if s.query.Limit > 0 {
		ordered = make([]Match, len(s.recent))
		for i := range s.recent {
			ordered[i] = s.recent[i].Match
		}
	} else {
		ordered = make([]Match, len(s.result.Matches))
		copy(ordered, s.result.Matches)
	}
	slices.SortStableFunc(ordered, compareMatches)
	slices.SortFunc(s.result.Issues, func(a, b Issue) int {
		if cmpSource := cmp.Compare(a.Source.Harness, b.Source.Harness); cmpSource != 0 {
			return cmpSource
		}
		if cmpPath := cmp.Compare(a.Path, b.Path); cmpPath != 0 {
			return cmpPath
		}
		return cmp.Compare(a.Message, b.Message)
	})
	for i := range ordered {
		ordered[i].Live = liveMatches(ordered[i].Conversation, s.query.Registry)
	}
	s.result.Matches = ordered
	return s.result
}

// compareMatches orders matches by most recently updated conversation first,
// then creation time, harness, path, and session ID. Zero timestamps sort last
// because no real timestamp precedes them.
func compareMatches(a, b Match) int {
	if order := b.Conversation.UpdatedAt.Compare(a.Conversation.UpdatedAt); order != 0 {
		return order
	}
	if order := b.Conversation.CreatedAt.Compare(a.Conversation.CreatedAt); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Conversation.Harness, b.Conversation.Harness); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Conversation.Path, b.Conversation.Path); order != 0 {
		return order
	}
	return cmp.Compare(a.Conversation.SessionID, b.Conversation.SessionID)
}

// dedupeSources canonicalizes each source path and removes later sources with
// the same harness and path, preserving first-seen order. Repeating a source must
// not duplicate its conversations, its coverage, or its share of a result limit.
// An empty path is left alone so it still reports ErrInvalidSource rather than
// silently resolving to the working directory, and a path that cannot be made
// absolute keeps its cleaned form; deduplication itself never fails.
func dedupeSources(sources []Source) []Source {
	unique := make([]Source, 0, len(sources))
	seen := make(map[Source]struct{}, len(sources))
	for _, source := range sources {
		if source.Path != "" {
			absolute, err := filepath.Abs(source.Path)
			if err != nil {
				absolute = filepath.Clean(source.Path)
			}
			source.Path = absolute
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		unique = append(unique, source)
	}
	return unique
}

func liveMatches(c Conversation, sessions []registry.Session) []LiveState {
	result := []LiveState{}
	for _, session := range sessions {
		if session.Harness != c.Harness {
			continue
		}
		sameID := session.SessionID != "" && session.SessionID == c.SessionID
		samePath := !isDatabase(c.Path) && session.SessionPath != "" && (filepath.Clean(session.SessionPath) == c.Path || registry.PathsEqual(session.SessionPath, c.Path))
		if (sameID && (session.SessionPath == "" || samePath)) || (samePath && session.SessionID == "") {
			result = append(result, LiveState{RegistryID: session.ID, Presence: session.Presence, UpdatedAt: session.UpdatedAt})
		}
	}
	return result
}

func (s *search) resultError() error {
	if len(s.result.Issues) == 0 {
		return nil
	}
	for _, source := range s.result.Sources {
		if source.Status == "unsupported" && (s.explicitSources || s.query.Harness != "") {
			return errors.Join(ErrIncomplete, ErrUnsupportedHarness)
		}
	}
	return ErrIncomplete
}
