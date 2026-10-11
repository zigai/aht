package history

import (
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

	native "github.com/zigai/aht/v2/internal/harness/transcript"

	"github.com/zigai/aht/v2/internal/pathmatch"
	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	defaultExcerpts = 3
	maxQueryBytes   = 4096
	maxRecordBytes  = native.MaxRecordBytes
	maxIssues       = 100
)

const (
	// SortUpdated orders by most recent activity first.
	SortUpdated Sort = "updated"
	// SortCreated orders by most recently started conversation first.
	SortCreated Sort = "created"
	// SortMatches orders by most matching message parts first. Search only.
	SortMatches Sort = "matches"
	// SortMessages orders by most user and assistant messages first.
	SortMessages Sort = "messages"
)

const (
	modeSearch scanMode = iota
	modeList
	modeRefresh
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
	// ErrIndexUnavailable reports that the disposable index could not be used.
	// Search, List, and Stream fall back to scanning native history; Refresh fails.
	ErrIndexUnavailable = errors.New("history index unavailable")
	errRecordSize       = native.ErrRecordSize
	errUnknownFormat    = native.ErrUnknownFormat
)

// Source identifies a harness's transcript directory or native SQLite database.
// Explicit sources replace default discovery, allowing custom profiles and exports.
type Source struct {
	Harness registry.Harness `json:"harness"`
	Path    string           `json:"path"`
}

// Sort orders results. The zero value orders by most recently updated conversation.
type Sort string

// Filter selects conversations by native metadata. Every set field must match.
// Dir and ExcludeDirs match recorded working directories or workspace roots and
// their descendants. Since keeps conversations active at or after Since; Until
// keeps conversations started at or before Until; conversations without native
// timestamps never match a time bound. GitBranch matches exactly; Model matches a
// case-insensitive substring. Presence is PresenceLive or PresenceGone and is
// evaluated against Registry, an optional snapshot also used for live-state
// enrichment; its absence means unknown.
type Filter struct {
	Harnesses       []registry.Harness `json:"harnesses,omitempty"`
	Dir             string             `json:"dir,omitempty"`
	ExcludeDirs     []string           `json:"exclude_dirs,omitempty"`
	Since           time.Time          `json:"since,omitzero"`
	Until           time.Time          `json:"until,omitzero"`
	GitBranch       string             `json:"git_branch,omitempty"`
	Model           string             `json:"model,omitempty"`
	MinMessages     int                `json:"min_messages,omitempty"`
	Presence        registry.Presence  `json:"presence,omitempty"`
	Registry        []registry.Session `json:"-"`
	IgnoreHarnesses []registry.Harness `json:"-"`
	IgnorePaths     []string           `json:"-"`
}

// Query searches conversation text. A conversation matches when every term
// matches at least one selected message part and no Exclude term matches any.
// Terms are literal and case-insensitive with Unicode case folding unless
// CaseSensitive is set. Regex treats terms and exclusions as Go regular
// expressions; Word requires matches to start and end at word boundaries.
// Regex and Word use Go regular-expression case folding.
//
// Excerpts caps excerpts per conversation; zero means three. Limit zero returns
// all matches; positive values keep the first Limit matches in Sort order and set
// Result.Truncated when more conversations matched.
type Query struct {
	Filter

	// Text is one more term; Harness selects one more harness.
	Text          string           `json:"text"`
	Harness       registry.Harness `json:"harness,omitempty"`
	Terms         []string         `json:"terms"`
	Exclude       []string         `json:"exclude,omitempty"`
	Regex         bool             `json:"regex"`
	Word          bool             `json:"word"`
	Role          string           `json:"role,omitempty"`
	IncludeTools  bool             `json:"include_tools"`
	CaseSensitive bool             `json:"case_sensitive"`
	Excerpts      int              `json:"excerpts,omitempty"`
	Sort          Sort             `json:"sort,omitempty"`
	Limit         int              `json:"limit"`
}

// ListQuery lists conversations selected by Filter. Limit and Sort behave as in
// Query; SortMatches is invalid.
type ListQuery struct {
	Filter

	Sort  Sort `json:"sort,omitempty"`
	Limit int  `json:"limit"`
}

// Catalog reads Sources, or default local sources when Sources is nil.
// A non-nil empty Sources slice searches nothing. IndexPath selects the disposable
// SQLite index; empty uses aht/history-v1.sqlite under [os.UserCacheDir].
// Progress, when set, is called from one goroutine at a time while sources are
// scanned; it must return quickly.
type Catalog struct {
	Sources   []Source
	IndexPath string
	Progress  func(Progress)
}

// Progress reports scanning of one source. Total counts the history files
// discovered in Source, Done those inspected so far, and Refreshed those whose
// index entries were rebuilt because the native history changed.
type Progress struct {
	Source    Source `json:"source"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Refreshed int    `json:"refreshed"`
}

// Conversation identifies native history, including histories never seen by AHT.
// Path is the transcript or database, not an AHT registry path. Title is the
// harness's native display name and Prompt the first meaningful user prompt.
// Messages counts user and assistant message parts. Unknown metadata is omitted;
// timestamps are native timestamps, never inferred from file mtimes.
type Conversation = native.Conversation

// Span is a byte range of Excerpt.Text that matched a query term.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Excerpt is a bounded window around the first match in a message. Matches lists
// every term match inside Text. Line is a one-based JSONL line, or zero for
// databases. MessageID is native when available. Role is user, assistant, or tool.
// Reasoning and system text are excluded.
type Excerpt struct {
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Spans     []Span    `json:"spans,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	Line      int       `json:"line,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}

// RegistryState is evidence from the caller's registry snapshot, not a fresh process
// probe. Several tracked incarnations may refer to one historical conversation.
type RegistryState struct {
	RegistryID string            `json:"registry_id"`
	Presence   registry.Presence `json:"presence"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Match is one selected conversation. Search fills Excerpts and MatchingParts,
// the number of message parts matching any term; List leaves them empty.
// ResumeCommand is the harness's native resume argv, to run in Conversation.CWD;
// it is empty when the harness cannot resume by identity. No RegistryStates
// entries mean presence is unknown, not that the conversation has terminated.
type Match struct {
	Conversation   Conversation    `json:"conversation"`
	Excerpts       []Excerpt       `json:"excerpts"`
	MatchingParts  int             `json:"matching_parts"`
	ResumeCommand  []string        `json:"resume_command,omitempty"`
	RegistryStates []RegistryState `json:"live"`
}

// SourceStatus reports a source's coverage: searched, skipped, missing,
// unsupported, or failed. Files counts inspected transcript files/databases.
type SourceStatus struct {
	Source Source `json:"source"`
	Status string `json:"status"`
	Files  int    `json:"files"`
}

// Issue identifies an unreadable, invalid, or bounded source. It never contains
// transcript content. Record marks one malformed or oversized record skipped
// inside an otherwise searched history; record issues alone never make a search
// incomplete. Diagnostics are bounded; additional issues are counted.
type Issue struct {
	Source  Source `json:"source"`
	Path    string `json:"path"`
	Message string `json:"message"`
	Record  bool   `json:"record,omitempty"`
}

// Result includes partial matches even when an error reports ErrIncomplete.
// Truncated means more conversations matched than Limit allowed. Sources
// distinguishes skipped, missing, and unsupported histories from searched ones.
type Result struct {
	Matches       []Match        `json:"matches"`
	Sources       []SourceStatus `json:"sources"`
	Issues        []Issue        `json:"issues"`
	OmittedIssues int            `json:"omitted_issues"`
	Truncated     bool           `json:"truncated"`
}

type scanMode int

type search struct {
	mu              sync.Mutex
	index           *historyIndex
	writer          *indexWriter
	indexErr        error
	explicitSources bool
	sourceMetadata  map[string]string
	mode            scanMode
	query           Query
	terms           []matcher
	exclude         []matcher
	stream          func(Match) bool
	stopped         bool
	progress        func(Progress)
	matched         int
	failures        int
	recent          matchHeap
	result          Result
}

type retainedMatch struct {
	Match

	indexID int64
}

// matchHeap holds the best Limit matches. The root is the least preferred
// match under the result ordering, so a full heap drops it in O(log limit) and
// memory stays O(limit) however many conversations match.
type matchHeap struct {
	items   []retainedMatch
	compare func(a, b Match) int
}

func (h *matchHeap) Len() int { return len(h.items) }

// Less orders the root at the least preferred match: [heap.Pop] yields the match
// a result limit would drop first.
func (h *matchHeap) Less(i, j int) bool { return h.compare(h.items[j].Match, h.items[i].Match) < 0 }

func (h *matchHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *matchHeap) Push(value any) {
	match, ok := value.(retainedMatch)
	if !ok {
		panic("history: matchHeap holds only retainedMatch values")
	}
	h.items = append(h.items, match)
}

func (h *matchHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items = h.items[:last]
	return value
}

// Validate checks the filter without reading any history.
func (f Filter) Validate() error {
	for _, id := range f.Harnesses {
		if _, err := harness.Parse(string(id)); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
		}
	}
	if f.MinMessages < 0 {
		return fmt.Errorf("%w: minimum messages must be nonnegative", ErrInvalidQuery)
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && f.Until.Before(f.Since) {
		return fmt.Errorf("%w: until precedes since", ErrInvalidQuery)
	}
	switch f.Presence {
	case "", registry.PresenceLive, registry.PresenceGone:
		return nil
	case registry.PresenceUnknown:
	}
	return fmt.Errorf("%w: invalid presence %q; choose live or gone", ErrInvalidQuery, f.Presence)
}

// Validate checks the public query contract without reading any history.
func (q Query) Validate() error {
	q = q.combined()
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if len(q.Terms) == 0 {
		return fmt.Errorf("%w: provide at least one search term", ErrInvalidQuery)
	}
	if err := validateTermText(slices.Concat(q.Terms, q.Exclude)); err != nil {
		return err
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: limit must be nonnegative (0 means unlimited)", ErrInvalidQuery)
	}
	if q.Excerpts < 0 {
		return fmt.Errorf("%w: excerpts must be nonnegative", ErrInvalidQuery)
	}
	if err := validateSort(q.Sort, true); err != nil {
		return err
	}
	if err := validateRole(q.Role); err != nil {
		return err
	}
	if _, err := compileMatchers(q.Terms, q); err != nil {
		return err
	}
	if _, err := compileMatchers(q.Exclude, q); err != nil {
		return err
	}
	return nil
}

// combined folds Text into Terms and Harness into Harnesses.
func (q Query) combined() Query {
	if q.Text != "" {
		q.Terms = slices.Concat([]string{q.Text}, q.Terms)
		q.Text = ""
	}
	if q.Harness != "" {
		q.Harnesses = slices.Concat(q.Harnesses, []registry.Harness{q.Harness})
		q.Harness = ""
	}
	return q
}

// Validate checks the list contract without reading any history.
func (q ListQuery) Validate() error {
	if err := q.Filter.Validate(); err != nil {
		return err
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: limit must be nonnegative (0 means unlimited)", ErrInvalidQuery)
	}
	return validateSort(q.Sort, false)
}

func (q ListQuery) searchQuery() Query {
	return Query{
		Filter:        q.Filter,
		Text:          "",
		Harness:       "",
		Terms:         nil,
		Exclude:       nil,
		Regex:         false,
		Word:          false,
		Role:          "",
		IncludeTools:  false,
		CaseSensitive: false,
		Excerpts:      0,
		Sort:          q.Sort,
		Limit:         q.Limit,
	}
}

// Search searches the machine's default local history sources.
func Search(ctx context.Context, query Query) (Result, error) {
	var catalog Catalog
	return catalog.Search(ctx, query)
}

func validateTermText(terms []string) error {
	for _, term := range terms {
		if strings.TrimSpace(term) == "" {
			return fmt.Errorf("%w: provide nonempty text", ErrInvalidQuery)
		}
		if len(term) > maxQueryBytes {
			return fmt.Errorf("%w: text exceeds 4096 bytes", ErrInvalidQuery)
		}
	}
	return nil
}

func validateRole(role string) error {
	if role != "" && !slices.Contains([]string{"user", "assistant", "agent", "tool", "all"}, role) {
		return fmt.Errorf("%w: invalid role %q; choose user, agent, assistant, tool, or all", ErrInvalidQuery, role)
	}
	return nil
}

func validateSort(sort Sort, search bool) error {
	switch sort {
	case "", SortUpdated, SortCreated, SortMessages:
		return nil
	case SortMatches:
		if search {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid sort %q", ErrInvalidQuery, sort)
}

// Search refreshes changed histories and searches the local index, falling back
// to a direct scan when the disposable index cannot be used. Sources and files
// retain discovery and lexical ordering, with bounded native record I/O. Matches
// are ordered by Query.Sort. Cancellation returns the partial result and the
// context error. Each call is independent; simultaneous calls are safe if callers
// do not mutate input slices.
func (c Catalog) Search(ctx context.Context, query Query) (Result, error) {
	var s search
	s.mode = modeSearch
	s.query = query
	return c.run(ctx, &s)
}

// List returns conversations selected by the filter as matches without
// excerpts, refreshing the index like Search.
func (c Catalog) List(ctx context.Context, query ListQuery) (Result, error) {
	var s search
	s.mode = modeList
	s.query = query.searchQuery()
	return c.run(ctx, &s)
}

// Stream calls yield with each matching conversation as soon as it is found, in
// discovery order rather than Sort order. Returning false stops the search
// without error. The returned Result carries coverage and diagnostics but no
// matches. Sort and Limit must be zero.
func (c Catalog) Stream(ctx context.Context, query Query, yield func(Match) bool) (Result, error) {
	if query.Limit != 0 || query.Sort != "" {
		return Result{Matches: []Match{}, Sources: []SourceStatus{}, Issues: []Issue{}, OmittedIssues: 0, Truncated: false}, fmt.Errorf("%w: streamed searches report matches in discovery order without sort or limit", ErrInvalidQuery)
	}
	var s search
	s.mode = modeSearch
	s.query = query
	s.stream = yield
	return c.run(ctx, &s)
}

// Refresh brings the disposable index up to date with the catalog's sources
// without searching. It returns ErrIndexUnavailable when the index cannot be used.
func (c Catalog) Refresh(ctx context.Context) (Result, error) {
	var s search
	s.mode = modeRefresh
	sources, err := c.begin(ctx, &s)
	if err != nil || len(sources) == 0 {
		return s.finalize(ctx), err
	}
	index, err := openHistoryIndex(ctx, c.IndexPath, s.indexSources(sources))
	if err != nil {
		return s.finalize(ctx), err
	}
	s.index = index
	runErr := s.runIndexed(ctx, sources)
	closeErr := index.close()
	s.index = nil
	return s.finalize(ctx), errors.Join(runErr, closeErr)
}

func (c Catalog) run(ctx context.Context, s *search) (Result, error) {
	sources, err := c.begin(ctx, s)
	if err != nil {
		return s.finalize(ctx), err
	}
	if len(sources) == 0 {
		return s.finalize(ctx), nil
	}
	err = c.searchIndexed(ctx, s, sources)
	return s.finalize(ctx), err
}

// searchDirect scans the sources without the disposable index. Both the fallback
// and the indexed path share this exact code, so it is the reference behavior
// the index must reproduce.
func (c Catalog) searchDirect(ctx context.Context, query Query) (Result, error) {
	var s search
	s.mode = modeSearch
	s.query = query
	return c.runDirect(ctx, &s)
}

func (c Catalog) listDirect(ctx context.Context, query ListQuery) (Result, error) {
	var s search
	s.mode = modeList
	s.query = query.searchQuery()
	return c.runDirect(ctx, &s)
}

func (c Catalog) runDirect(ctx context.Context, s *search) (Result, error) {
	sources, err := c.begin(ctx, s)
	if err != nil {
		return s.finalize(ctx), err
	}
	if len(sources) == 0 {
		return s.finalize(ctx), nil
	}
	err = s.runDirect(ctx, sources)
	return s.finalize(ctx), err
}

// searchIndexed searches through the disposable index and falls back to the
// direct scan when the index is unavailable, either at open time or while
// refreshing a source. A failed attempt is discarded before the fallback so
// partially indexed matches cannot be reported twice. A streamed search cannot
// retract matches it already reported, so it fails instead of falling back
// once anything was streamed.
func (c Catalog) searchIndexed(ctx context.Context, s *search, sources []Source) error {
	index, err := openHistoryIndex(ctx, c.IndexPath, s.indexSources(sources))
	if err != nil {
		if !errors.Is(err, ErrIndexUnavailable) {
			return err
		}
		return s.runDirect(ctx, sources)
	}
	s.index = index
	runErr := s.runIndexed(ctx, sources)
	closeErr := index.close()
	s.index = nil
	if errors.Is(runErr, ErrIndexUnavailable) {
		if s.stream != nil && s.matched > 0 {
			return runErr
		}
		// The index is unusable, so its own close error is irrelevant.
		s.reset()
		return s.runDirect(ctx, sources)
	}
	return errors.Join(runErr, closeErr)
}

// begin validates the query, resolves the sources to scan, and clears the result
// every path records into. Sources are deduplicated so repeating one cannot
// duplicate its conversations.
func (c Catalog) begin(ctx context.Context, s *search) ([]Source, error) {
	s.explicitSources = c.Sources != nil
	s.progress = c.Progress
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
	var excerptErr error
	if s.mode != modeRefresh {
		excerptErr = s.index.readRetained(ctx, s)
	}
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
		if s.halted() {
			return nil
		}
		reason, ok := s.selectSource(source)
		if ok {
			selected++
			s.scanSource(ctx, source)
			continue
		}
		s.result.Sources = append(s.result.Sources, SourceStatus{Source: source, Status: "skipped", Files: 0})
		skipped = append(skipped, Issue{Source: source, Path: source.Path, Message: reason, Record: false})
	}
	if selected == 0 && len(sources) > 0 {
		for _, issue := range skipped {
			s.recordIssue(issue)
		}
	}
	return nil
}

func (s *search) prepare() error {
	if err := s.validate(); err != nil {
		return err
	}
	s.query = s.query.combined()
	s.query.Harnesses = uniqueHarnesses(s.query.Harnesses)
	if err := s.absolutePaths(); err != nil {
		return err
	}
	switch s.query.Role {
	case "agent":
		s.query.Role = "assistant"
	case "all":
		s.query.Role = ""
	case "tool":
		s.query.IncludeTools = true
	}
	if s.query.Excerpts == 0 {
		s.query.Excerpts = defaultExcerpts
	}
	s.recent.compare = resultOrder(s.query.Sort)
	s.terms, _ = compileMatchers(s.query.Terms, s.query)
	s.exclude, _ = compileMatchers(s.query.Exclude, s.query)
	return nil
}

func (s *search) validate() error {
	switch s.mode {
	case modeSearch:
		return s.query.Validate()
	case modeList:
		return (ListQuery{Filter: s.query.Filter, Sort: s.query.Sort, Limit: s.query.Limit}).Validate()
	case modeRefresh:
	}
	return nil
}

func uniqueHarnesses(ids []registry.Harness) []registry.Harness {
	harnesses := make([]registry.Harness, 0, len(ids))
	for _, id := range ids {
		parsed, _ := harness.Parse(string(id))
		if !slices.Contains(harnesses, parsed) {
			harnesses = append(harnesses, parsed)
		}
	}
	return harnesses
}

func (s *search) absolutePaths() error {
	if s.query.Dir != "" {
		path, err := filepath.Abs(s.query.Dir)
		if err != nil {
			return fmt.Errorf("search directory: %w", err)
		}
		s.query.Dir = path
	}
	excluded := make([]string, 0, len(s.query.ExcludeDirs))
	for _, dir := range s.query.ExcludeDirs {
		path, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("exclude directory: %w", err)
		}
		excluded = append(excluded, path)
	}
	s.query.ExcludeDirs = excluded
	return nil
}

// reset clears the result state so a discarded attempt cannot leak matches,
// coverage, or diagnostics into the next one.
func (s *search) reset() {
	s.matched = 0
	s.failures = 0
	s.recent.items = nil
	s.indexErr = nil
	s.stopped = false
	s.result.Matches = []Match{}
	s.result.Sources = []SourceStatus{}
	s.result.Issues = []Issue{}
	s.result.OmittedIssues = 0
	s.result.Truncated = false
}

// selectSource reports whether the source is selected and, when it is not, the
// reason recorded in the skipped coverage and diagnostics.
func (s *search) selectSource(source Source) (string, bool) {
	if len(s.query.Harnesses) > 0 {
		if !slices.Contains(s.query.Harnesses, source.Harness) {
			return fmt.Sprintf("source skipped: harness %q is not among the requested harnesses", source.Harness), false
		}
		return "", true
	}
	if slices.Contains(s.query.IgnoreHarnesses, source.Harness) {
		return fmt.Sprintf("source skipped: harness %q is excluded by the ignore list", source.Harness), false
	}
	return "", true
}

func (s *search) issue(source Source, path string, err error) {
	s.recordIssue(Issue{Source: source, Path: path, Message: err.Error(), Record: false})
}

// recordProblem reports one skipped record of a history that was otherwise read.
func (s *search) recordProblem(source Source, path string, err error) {
	s.recordIssue(Issue{Source: source, Path: path, Message: err.Error(), Record: true})
}

func (s *search) recordIssue(issue Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !issue.Record {
		s.failures++
	}
	if len(s.result.Issues) >= maxIssues {
		s.result.OmittedIssues++
		return
	}
	s.result.Issues = append(s.result.Issues, issue)
}

// halted reports whether a streaming caller asked to stop.
func (s *search) halted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func within(path, parent string) bool {
	if path == "" || parent == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// activeUntil and activeSince bound a conversation's native activity, falling
// back to the other timestamp when only one is known.
func activeUntil(c Conversation) time.Time {
	if c.UpdatedAt.IsZero() {
		return c.CreatedAt
	}
	return c.UpdatedAt
}

func activeSince(c Conversation) time.Time {
	if c.CreatedAt.IsZero() {
		return c.UpdatedAt
	}
	return c.CreatedAt
}

// accepts applies the metadata filter. Index queries prefilter with weaker SQL
// predicates, so this is the authority for both scan paths.
func (s *search) accepts(c Conversation, states []RegistryState) bool {
	f := s.query.Filter
	if !f.acceptsDirs(c) || !f.acceptsTimes(c) {
		return false
	}
	if f.GitBranch != "" && c.GitBranch != f.GitBranch {
		return false
	}
	if f.Model != "" && !strings.Contains(strings.ToLower(c.Model), strings.ToLower(f.Model)) {
		return false
	}
	if c.Messages < f.MinMessages {
		return false
	}
	switch f.Presence {
	case registry.PresenceLive:
		return presence(states) == registry.PresenceLive
	case registry.PresenceGone:
		return presence(states) == registry.PresenceGone
	case registry.PresenceUnknown:
	}
	return true
}

func (f Filter) acceptsDirs(c Conversation) bool {
	if f.Dir != "" && !within(c.CWD, f.Dir) && !within(c.ProjectRoot, f.Dir) {
		return false
	}
	for _, dir := range f.ExcludeDirs {
		if within(c.CWD, dir) || within(c.ProjectRoot, dir) {
			return false
		}
	}
	for _, path := range f.IgnorePaths {
		if pathmatch.Match(c.CWD, path) || pathmatch.Match(c.ProjectRoot, path) {
			return false
		}
	}
	return true
}

func (f Filter) acceptsTimes(c Conversation) bool {
	if !f.Since.IsZero() && (activeUntil(c).IsZero() || activeUntil(c).Before(f.Since)) {
		return false
	}
	return f.Until.IsZero() || (!activeSince(c).IsZero() && !activeSince(c).After(f.Until))
}

// presence summarizes registry evidence: live when any tracked incarnation is
// live, gone when every one is gone, and unknown otherwise.
func presence(states []RegistryState) registry.Presence {
	allGone := len(states) > 0
	for _, state := range states {
		if state.Presence == registry.PresenceLive {
			return registry.PresenceLive
		}
		if state.Presence != registry.PresenceGone {
			allGone = false
		}
	}
	if allGone {
		return registry.PresenceGone
	}
	return registry.PresenceUnknown
}

// add accepts a parsed conversation: an index refresh stores it, and a scan
// keeps it when it satisfies every term and no exclusion.
func (s *search) add(ctx context.Context, t *transcript) {
	if s.writer != nil {
		s.writer.finish(t.match.Conversation)
		return
	}
	if s.mode == modeSearch && !t.qualifies(len(s.terms)) {
		return
	}
	s.keep(ctx, t.match, 0)
}

// keep ranks matches. Indexed searches with a limit pass the index ID so only
// retained matches load excerpts; direct scans pass zero because they already
// have their excerpts. A positive limit never stops the scan: the heap holds
// the best Limit matches seen so far, and Truncated records that more matched.
func (s *search) keep(ctx context.Context, m Match, indexID int64) {
	if s.mode == modeRefresh || m.Conversation.SessionID == "" || (s.mode == modeSearch && m.MatchingParts == 0) {
		return
	}
	states := registryStates(m.Conversation, s.query.Registry)
	if !s.accepts(m.Conversation, states) {
		return
	}
	m.RegistryStates = states
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.matched++
	if s.stream != nil {
		enriched := []Match{m}
		enrich(ctx, enriched)
		if !s.stream(enriched[0]) {
			s.stopped = true
		}
		return
	}
	s.retain(m, indexID)
}

func (s *search) retain(m Match, indexID int64) {
	if s.query.Limit == 0 {
		s.result.Matches = append(s.result.Matches, m)
		return
	}
	if s.recent.Len() < s.query.Limit {
		heap.Push(&s.recent, retainedMatch{Match: m, indexID: indexID})
	} else if s.recent.compare(m, s.recent.items[0].Match) < 0 {
		s.recent.items[0] = retainedMatch{Match: m, indexID: indexID}
		heap.Fix(&s.recent, 0)
	}
	if s.matched > s.query.Limit {
		s.result.Truncated = true
	}
}

// finalize orders the accepted matches and returns the result. Call it once per
// search on every path that returns a result; Matches is always non-nil.
func (s *search) finalize(ctx context.Context) Result {
	var ordered []Match
	if s.query.Limit > 0 {
		ordered = make([]Match, len(s.recent.items))
		for i := range s.recent.items {
			ordered[i] = s.recent.items[i].Match
		}
	} else {
		ordered = make([]Match, len(s.result.Matches))
		copy(ordered, s.result.Matches)
	}
	slices.SortStableFunc(ordered, resultOrder(s.query.Sort))
	slices.SortFunc(s.result.Issues, func(a, b Issue) int {
		if cmpSource := cmp.Compare(a.Source.Harness, b.Source.Harness); cmpSource != 0 {
			return cmpSource
		}
		if cmpPath := cmp.Compare(a.Path, b.Path); cmpPath != 0 {
			return cmpPath
		}
		return cmp.Compare(a.Message, b.Message)
	})
	if ctx.Err() == nil {
		enrich(ctx, ordered)
	}
	s.result.Matches = ordered
	return s.result
}

// resultOrder orders matches by the requested key, then by most recently updated
// conversation, creation time, harness, path, and session ID. Zero timestamps
// sort last because no real timestamp precedes them.
func resultOrder(sort Sort) func(a, b Match) int {
	return func(a, b Match) int {
		switch sort {
		case SortCreated:
			if order := b.Conversation.CreatedAt.Compare(a.Conversation.CreatedAt); order != 0 {
				return order
			}
		case SortMatches:
			if order := cmp.Compare(b.MatchingParts, a.MatchingParts); order != 0 {
				return order
			}
		case SortMessages:
			if order := cmp.Compare(b.Conversation.Messages, a.Conversation.Messages); order != 0 {
				return order
			}
		case "", SortUpdated:
		}
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

func registryStates(c Conversation, sessions []registry.Session) []RegistryState {
	result := []RegistryState{}
	for _, session := range sessions {
		if session.Harness != c.Harness {
			continue
		}
		sameID := session.SessionID != "" && session.SessionID == c.SessionID
		samePath := !isDatabase(c.Path) && session.SessionPath != "" && (filepath.Clean(session.SessionPath) == c.Path || registry.PathsEqual(session.SessionPath, c.Path))
		if (sameID && (session.SessionPath == "" || samePath)) || (samePath && session.SessionID == "") {
			result = append(result, RegistryState{RegistryID: session.ID, Presence: session.Presence(), UpdatedAt: session.UpdatedAt})
		}
	}
	return result
}

func (s *search) resultError() error {
	if s.failures == 0 {
		return nil
	}
	for _, source := range s.result.Sources {
		if source.Status == "unsupported" && (s.explicitSources || len(s.query.Harnesses) > 0) {
			return errors.Join(ErrIncomplete, ErrUnsupportedHarness)
		}
	}
	return ErrIncomplete
}
