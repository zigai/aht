package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	native "github.com/zigai/aht/v2/internal/harness/transcript"
)

const directoryScanThreshold = 32

// Conversations without a native timestamp store sentinels that no time bound
// can satisfy.
const (
	unknownActive  = math.MinInt64
	unknownStarted = math.MaxInt64
)

type indexQueries struct {
	conversations *sql.Stmt
	parts         *sql.Stmt
}

// indexedMatches plans one conversation's matches: the number of distinct
// matching parts, the first parts used for excerpts, and which terms matched.
type indexedMatches struct {
	count int
	ids   []int64
	hits  []bool
}

// groupFile is one indexed history of a parent history and its children;
// changed means this search stored it.
type groupFile struct {
	file    indexedFile
	changed bool
}

type indexedConversation struct {
	id           int64
	conversation Conversation
}

func (plan *indexedMatches) qualifies() bool {
	return plan != nil && plan.count > 0 && !slices.Contains(plan.hits, false)
}

// prepareQuery computes the candidate conversations once per search. It reads the
// committed index in autocommit: each changed file is refreshed and committed
// before the matches of that file are read.
func (index *historyIndex) prepareQuery(ctx context.Context, s *search) (err error) {
	if index.queries != nil {
		return nil
	}
	if index.unavailable != nil {
		return index.unavailable
	}
	index.candidates = map[int64]bool{}
	index.plans = map[int64]*indexedMatches{}
	index.excluded = map[int64]bool{}
	if s.mode == modeSearch {
		if err := index.findParts(ctx, s, 0); err != nil {
			return err
		}
	}
	index.queries = new(indexQueries)
	defer func() {
		if err != nil {
			err = errors.Join(err, index.queries.close())
			index.queries = nil
		}
	}()
	index.queries.conversations, err = index.conn.PrepareContext(ctx, "SELECT id,metadata FROM conversations WHERE file_id=? ORDER BY id")
	if err != nil {
		return index.contention(fmt.Errorf("prepare indexed conversations: %w", err))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", max(s.query.Excerpts, 1)), ",")
	//nolint:gosec // G202: the statement only repeats fixed placeholders.
	index.queries.parts, err = index.conn.PrepareContext(ctx, "SELECT id,role,body,message_id,line,timestamp FROM parts WHERE id IN ("+placeholders+")")
	if err != nil {
		return index.contention(fmt.Errorf("prepare indexed text: %w", err))
	}
	return nil
}

// findParts plans every conversation with a part matching a term, scoped to one
// file when fileID is nonzero, and records conversations with excluded text.
func (index *historyIndex) findParts(ctx context.Context, s *search, fileID int64) error {
	found := map[int64][]int64{}
	hits := map[int64][]bool{}
	for term, m := range s.terms {
		err := index.eachPart(ctx, s, m, fileID, func(file, conversation, part int64) {
			index.candidates[file] = true
			found[conversation] = append(found[conversation], part)
			if hits[conversation] == nil {
				hits[conversation] = make([]bool, len(s.terms))
			}
			hits[conversation][term] = true
		})
		if err != nil {
			return err
		}
	}
	for _, m := range s.exclude {
		if err := index.eachPart(ctx, s, m, fileID, func(_, conversation, _ int64) { index.excluded[conversation] = true }); err != nil {
			return err
		}
	}
	for conversation, parts := range found {
		slices.Sort(parts)
		parts = slices.Compact(parts)
		ids := make([]int64, s.query.Excerpts)
		copy(ids, parts)
		index.plans[conversation] = &indexedMatches{count: len(parts), ids: ids, hits: hits[conversation]}
	}
	return nil
}

func (index *historyIndex) eachPart(ctx context.Context, s *search, m matcher, fileID int64, visit func(file, conversation, part int64)) (err error) {
	query, args, err := index.partQuery(ctx, s, m, fileID)
	if err != nil {
		return err
	}
	rows, err := index.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return index.contention(fmt.Errorf("find indexed candidates: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var file, conversation, part int64
		var body, folded string
		targets := []any{&file, &conversation, &part}
		if m.verifies() {
			targets = append(targets, &body, &folded)
		}
		if err = rows.Scan(targets...); err != nil {
			return fmt.Errorf("read indexed candidate: %w", err)
		}
		if m.verifies() {
			normalized := folded
			if s.query.CaseSensitive {
				normalized = body
			}
			if !m.matches(body, normalized) {
				continue
			}
		}
		visit(file, conversation, part)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("scan indexed candidates: %w", err)
	}
	return nil
}

func (index *historyIndex) partQuery(ctx context.Context, s *search, m matcher, fileID int64) (string, []any, error) {
	predicate, args := index.candidatePredicate(s, fileID)
	phrase := trigramPhrase(fold(m.literal))
	trigrams := phrase != ""
	if trigrams {
		literal, err := index.preferLiteral(ctx, predicate, args, phrase, s.query.Dir != "" || fileID != 0)
		if err != nil {
			return "", nil, err
		}
		trigrams = !literal
	}
	columns := "c.file_id,c.id,p.id"
	if m.verifies() {
		columns += ",p.body,p.folded"
	}
	query := "SELECT " + columns + " FROM conversations c JOIN files f ON f.id=c.file_id " + partJoin(trigrams, s.query.CaseSensitive) + " ON p.conversation_id=c.id WHERE " + predicate
	if trigrams {
		query += " AND p.id IN (SELECT rowid FROM parts_fts WHERE parts_fts MATCH ?)"
		args = append(args, phrase)
	}
	column := "p.folded"
	if s.query.CaseSensitive {
		column = "p.body"
	}
	if m.literal != "" && (!trigrams || s.query.CaseSensitive) {
		query += " AND instr(" + column + ",?)>0"
		args = append(args, m.literal)
	}
	query += " ORDER BY c.id,p.id"
	return query, args, nil
}

// candidatePredicate selects the conversations a part query may read.
// Normalize with the same Go mapping that stored the folded column, not
// SQLite's different Unicode folding rules. A quoted trigram phrase matches
// the exact normalized substring. Case-sensitive, short, and NUL-containing
// literals additionally use literal matching; regular expressions and word
// boundaries are checked in Go, which also builds excerpts.
func (index *historyIndex) candidatePredicate(s *search, fileID int64) (string, []any) {
	filter, args := index.selectionSQL(s.query.Filter)
	// Tool parts are stored for opt-in searches only, so a default query must
	// exclude them from candidate plans in both the trigram and instr branches.
	predicate, args := rolePredicate(filter, s.query.Role, s.query.IncludeTools, args)
	// A selected literal scan avoids enumerating common trigrams across the
	// whole index. Indexed text lengths account for oversized message bodies;
	// CROSS JOIN keeps metadata selection ahead of reading those bodies.
	if fileID != 0 {
		predicate += " AND c.file_id=?"
		args = append(args, fileID)
	}
	return predicate, args
}

func partJoin(trigrams, caseSensitive bool) string {
	if !trigrams {
		return "CROSS JOIN parts p"
	}
	if caseSensitive {
		return "JOIN parts p"
	}
	return "JOIN parts p INDEXED BY parts_search"
}

// selectionSQL narrows candidates by the stored metadata columns. Bounds use
// whole seconds and never reject a conversation that accepts would keep;
// accepts remains the authority for every filter. Child histories are judged
// by their parent's metadata, so only the source and harness narrow them.
func (index *historyIndex) selectionSQL(f Filter) (string, []any) {
	metadata, metadataArgs := directorySQL(f.Dir)
	if !f.Since.IsZero() {
		metadata += " AND c.active>=?"
		metadataArgs = append(metadataArgs, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		metadata += " AND c.started<=?"
		metadataArgs = append(metadataArgs, f.Until.Unix())
	}
	if f.MinMessages > 0 {
		metadata += " AND c.messages>=?"
		metadataArgs = append(metadataArgs, f.MinMessages)
	}
	if f.GitBranch != "" {
		metadata += " AND c.branch=?"
		metadataArgs = append(metadataArgs, f.GitBranch)
	}
	filter := index.sourceFilter
	args := append([]any{}, index.sourceArgs...)
	if len(f.Harnesses) > 0 {
		filter += " AND f.harness IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.Harnesses)), ",") + ")"
		for _, id := range f.Harnesses {
			args = append(args, string(id))
		}
	}
	if metadata != "1" {
		filter += " AND (f.child=1 OR (" + metadata + "))"
		args = append(args, metadataArgs...)
	}
	return filter, args
}

func trigramPhrase(text string) string {
	if utf8.RuneCountInString(text) < 3 || strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return ""
	}
	return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
}

func (index *historyIndex) smallSelection(ctx context.Context, filter string, args []any) (bool, error) {
	var count int
	query := "SELECT count(*) FROM (SELECT p.id FROM conversations c JOIN files f ON f.id=c.file_id CROSS JOIN parts p ON p.conversation_id=c.id WHERE " + filter + " LIMIT ?)"
	values := append(append([]any{}, args...), directoryScanThreshold+1)
	if err := index.conn.QueryRowContext(ctx, query, values...).Scan(&count); err != nil {
		return false, index.contention(fmt.Errorf("select indexed parts: %w", err))
	}
	return count <= directoryScanThreshold, nil
}

func (index *historyIndex) preferLiteral(ctx context.Context, filter string, args []any, phrase string, scoped bool) (bool, error) {
	if !scoped {
		return index.smallSelection(ctx, filter, args)
	}
	var pages int64
	query := "SELECT coalesce(sum(1+length(CAST(p.folded AS BLOB))/(SELECT page_size FROM pragma_page_size)),0) FROM conversations c JOIN files f ON f.id=c.file_id CROSS JOIN parts p INDEXED BY parts_conversation ON p.conversation_id=c.id WHERE " + filter
	if err := index.conn.QueryRowContext(ctx, query, args...).Scan(&pages); err != nil {
		return false, index.contention(fmt.Errorf("measure selected parts: %w", err))
	}
	if pages <= directoryScanThreshold {
		return true, nil
	}
	var candidates int64
	if err := index.conn.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT rowid FROM parts_fts WHERE parts_fts MATCH ? LIMIT ?)", phrase, pages+1).Scan(&candidates); err != nil {
		return false, index.contention(fmt.Errorf("measure trigram candidates: %w", err))
	}
	return candidates > pages, nil
}

func rolePredicate(filter, role string, includeTools bool, args []any) (string, []any) {
	if role != "" {
		return "(" + filter + ") AND p.role = ?", append(args, role)
	}
	if !includeTools {
		return "(" + filter + ") AND p.role <> 'tool'", args
	}
	return filter, args
}

// matches reads one history group. Child conversations with the parent's
// session identity fold their text matches into the parent conversation; any
// other child conversation is matched on its own.
func (index *historyIndex) matches(ctx context.Context, s *search, group []groupFile) error {
	if s.mode == modeRefresh {
		return nil
	}
	if s.mode == modeSearch {
		candidate, err := index.groupCandidate(ctx, s, group)
		if err != nil || !candidate {
			return err
		}
	}
	conversations, err := index.foldGroup(ctx, group)
	if err != nil {
		return err
	}
	return index.collectMatches(ctx, s, conversations)
}

// groupCandidate refreshes the plans of changed files and reports whether any
// file of the group has a matching part.
func (index *historyIndex) groupCandidate(ctx context.Context, s *search, group []groupFile) (bool, error) {
	candidate := false
	for _, member := range group {
		if member.changed {
			if err := index.refreshPlans(ctx, s, member.file.id); err != nil {
				return false, err
			}
		}
		candidate = candidate || index.candidates[member.file.id]
	}
	return candidate, nil
}

// foldGroup returns the parent's conversations, with the plans of children
// that share their session identity folded in, followed by the remaining child
// conversations.
func (index *historyIndex) foldGroup(ctx context.Context, group []groupFile) ([]indexedConversation, error) {
	conversations, err := index.readConversations(ctx, group[0].file.id)
	if err != nil {
		return nil, err
	}
	parents := len(conversations)
	for _, member := range group[1:] {
		children, err := index.readConversations(ctx, member.file.id)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			owner := ownerConversation(conversations[:parents], child.conversation.SessionID)
			if owner == 0 {
				conversations = append(conversations, child)
				continue
			}
			index.plans[owner] = foldPlan(index.plans[owner], index.plans[child.id])
			index.excluded[owner] = index.excluded[owner] || index.excluded[child.id]
		}
	}
	return conversations, nil
}

func ownerConversation(conversations []indexedConversation, sessionID string) int64 {
	for _, c := range conversations {
		if c.conversation.SessionID == sessionID {
			return c.id
		}
	}
	return 0
}

// foldPlan adds a child conversation's plan to its owner's. Excerpt parts keep
// the owner's first, then the child's, like a direct scan reading the files in
// that order.
func foldPlan(owner, child *indexedMatches) *indexedMatches {
	if child == nil {
		return owner
	}
	if owner == nil {
		owner = &indexedMatches{count: 0, ids: make([]int64, len(child.ids)), hits: make([]bool, len(child.hits))}
	}
	owner.count += child.count
	for i, hit := range child.hits {
		owner.hits[i] = owner.hits[i] || hit
	}
	used := slices.Index(owner.ids, 0)
	if used < 0 {
		return owner
	}
	for _, id := range child.ids {
		if id == 0 || used == len(owner.ids) {
			break
		}
		owner.ids[used] = id
		used++
	}
	return owner
}

func (index *historyIndex) readConversations(ctx context.Context, fileID int64) ([]indexedConversation, error) {
	var conversations []indexedConversation
	err := index.eachConversation(ctx, fileID, func(c indexedConversation) { conversations = append(conversations, c) })
	return conversations, err
}

func (index *historyIndex) eachConversation(ctx context.Context, fileID int64, visit func(indexedConversation)) (err error) {
	rows, err := index.queries.conversations.QueryContext(ctx, fileID)
	if err != nil {
		return index.contention(fmt.Errorf("query indexed conversations: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var c indexedConversation
		var metadata string
		if err := rows.Scan(&c.id, &metadata); err != nil {
			return fmt.Errorf("read indexed conversation: %w", err)
		}
		if err := json.Unmarshal([]byte(metadata), &c.conversation); err != nil {
			return fmt.Errorf("decode indexed conversation: %w", err)
		}
		visit(c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan indexed conversations: %w", err)
	}
	return nil
}

func (index *historyIndex) collectMatches(ctx context.Context, s *search, conversations []indexedConversation) error {
	for _, c := range conversations {
		plan := index.plans[c.id]
		if s.mode == modeSearch && (index.excluded[c.id] || !plan.qualifies()) {
			continue
		}
		match := Match{Conversation: c.conversation, Excerpts: nil, MatchingParts: 0, ResumeCommand: nil, RegistryStates: nil}
		if s.mode == modeList || s.query.Limit > 0 {
			if plan != nil {
				match.MatchingParts = plan.count
			}
			s.keep(ctx, match, c.id)
			continue
		}
		if err := index.readExcerpts(ctx, s, c.id, &match); err != nil {
			return err
		}
		s.keep(ctx, match, 0)
	}
	return nil
}

// readRetained hydrates only the final heap after every selected history has
// been checked. Failed or canceled reads cannot expose metadata-only matches.
func (index *historyIndex) readRetained(ctx context.Context, s *search) error {
	if s.mode != modeSearch {
		return nil
	}
	retained := s.recent.items
	s.recent.items = nil
	for _, match := range retained {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read retained excerpts: %w", err)
		}
		if err := index.readExcerpts(ctx, s, match.indexID, &match.Match); err != nil {
			// Re-run native discovery to preserve source coverage and diagnostics
			// if the deferred index read fails after the source walk has finished.
			return indexUnavailable(err)
		}
		s.recent.items = append(s.recent.items, match)
	}
	return nil
}

// readExcerpts matches the plan's parts in plan order, which folded child
// conversations extend after the owner's own parts.
func (index *historyIndex) readExcerpts(ctx context.Context, s *search, id int64, match *Match) error {
	plan := index.plans[id]
	parts, err := index.readParts(ctx, plan.ids)
	if err != nil {
		return err
	}
	var t transcript
	t.match = *match
	for _, part := range plan.ids {
		if excerpt, ok := parts[part]; ok {
			s.matchText(&t, excerpt.Role, excerpt.Text, excerpt.MessageID, excerpt.Line, excerpt.Timestamp)
		}
	}
	t.match.MatchingParts = plan.count
	*match = t.match
	return nil
}

func (index *historyIndex) readParts(ctx context.Context, ids []int64) (map[int64]Excerpt, error) {
	parts := make(map[int64]Excerpt, len(ids))
	err := index.eachPartText(ctx, ids, func(id int64, excerpt Excerpt) { parts[id] = excerpt })
	return parts, err
}

func (index *historyIndex) eachPartText(ctx context.Context, ids []int64, visit func(int64, Excerpt)) (err error) {
	args := make([]any, len(ids))
	for i, part := range ids {
		args[i] = part
	}
	rows, err := index.queries.parts.QueryContext(ctx, args...)
	if err != nil {
		return index.contention(fmt.Errorf("query indexed text: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var part int64
		var excerpt Excerpt
		var timestamp string
		if err := rows.Scan(&part, &excerpt.Role, &excerpt.Text, &excerpt.MessageID, &excerpt.Line, &timestamp); err != nil {
			return fmt.Errorf("read indexed text: %w", err)
		}
		excerpt.Timestamp = native.NativeTime(timestamp)
		visit(part, excerpt)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan indexed text: %w", err)
	}
	return nil
}

func (index *historyIndex) refreshPlans(ctx context.Context, s *search, fileID int64) (err error) {
	// Refresh plans for rewritten/appended conversations before using them.
	rows, err := index.conn.QueryContext(ctx, "SELECT id FROM conversations WHERE file_id=?", fileID)
	if err != nil {
		return index.contention(fmt.Errorf("refresh indexed candidates: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return fmt.Errorf("read refreshed candidate: %w", err)
		}
		delete(index.plans, id)
		delete(index.excluded, id)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("scan refreshed candidates: %w", err)
	}
	return index.findParts(ctx, s, fileID)
}

func (queries *indexQueries) close() error {
	var err error
	for _, stmt := range []*sql.Stmt{queries.conversations, queries.parts} {
		if stmt != nil {
			err = errors.Join(err, stmt.Close())
		}
	}
	if err != nil {
		return fmt.Errorf("close indexed queries: %w", err)
	}
	return nil
}
