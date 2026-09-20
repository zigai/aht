package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const directoryScanThreshold = 32

type indexQueries struct {
	conversations *sql.Stmt
	parts         *sql.Stmt
}

type indexedMatches struct {
	count int
	ids   [maxExcerpts]int64
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
	index.plans = map[int64]indexedMatches{}
	if err := index.findParts(ctx, s, 0); err != nil {
		return err
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
	index.queries.parts, err = index.conn.PrepareContext(ctx, "SELECT role,body,message_id,line,timestamp FROM parts WHERE id IN (?,?,?) ORDER BY id")
	if err != nil {
		return index.contention(fmt.Errorf("prepare indexed text: %w", err))
	}
	return nil
}

func (index *historyIndex) findParts(ctx context.Context, s *search, fileID int64) (err error) {
	query, args, err := index.partQuery(ctx, s, fileID)
	if err != nil {
		return err
	}
	rows, err := index.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return index.contention(fmt.Errorf("find indexed candidates: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var previous int64
	var plan indexedMatches
	for rows.Next() {
		var file, conversation, part int64
		if err = rows.Scan(&file, &conversation, &part); err != nil {
			return fmt.Errorf("read indexed candidate: %w", err)
		}
		index.candidates[file] = true
		if previous != conversation {
			var next indexedMatches
			plan = next
			previous = conversation
		}
		if plan.count < maxExcerpts {
			plan.ids[plan.count] = part
		}
		plan.count++
		index.plans[conversation] = plan
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("scan indexed candidates: %w", err)
	}
	return nil
}

func (index *historyIndex) partQuery(ctx context.Context, s *search, fileID int64) (string, []any, error) {
	// Normalize with the same Go mapping that stored the folded column, not
	// SQLite's different Unicode folding rules. A quoted trigram phrase matches
	// the exact normalized substring. Case-sensitive, short, and NUL-containing
	// queries additionally use literal matching; the Go matcher still builds
	// excerpts.
	folded := fold(s.query.Text)
	filter, args := directorySQL(s.query.Dir)
	// For a small selected directory, reading its few parts is cheaper than
	// enumerating a common trigram across the entire index. CROSS JOIN keeps
	// SQLite's metadata selection ahead of reading message bodies in this path.
	small, err := index.smallDirectory(ctx, s.query.Dir, filter, args)
	if err != nil {
		return "", nil, err
	}
	// Tool parts are stored for opt-in searches only, so a default query must
	// exclude them from candidate plans in both the trigram and instr branches.
	predicate := filter
	if !s.query.IncludeTools {
		predicate = "(" + filter + ") AND p.role <> 'tool'"
	}
	query := "SELECT c.file_id,c.id,p.id FROM conversations c JOIN files f ON f.id=c.file_id CROSS JOIN parts p ON p.conversation_id=c.id WHERE " + predicate
	trigrams := false
	if fileID != 0 {
		query += " AND c.file_id=?"
		args = append(args, fileID)
	} else if !small && utf8.RuneCountInString(folded) >= 3 && !strings.ContainsRune(folded, 0) && utf8.ValidString(folded) {
		trigrams = true
		query = strings.Replace(query, "CROSS JOIN parts", "JOIN parts", 1)
		query += " AND p.id IN (SELECT rowid FROM parts_fts WHERE parts_fts MATCH ?)"
		args = append(args, `"`+strings.ReplaceAll(folded, `"`, `""`)+`"`)
	}
	column, literal := "p.folded", folded
	if s.query.CaseSensitive {
		column, literal = "p.body", s.query.Text
	}
	if !trigrams || s.query.CaseSensitive {
		query += " AND instr(" + column + ",?)>0"
		args = append(args, literal)
	}
	query += " ORDER BY c.id,p.id"
	return query, args, nil
}

func (index *historyIndex) smallDirectory(ctx context.Context, dir, filter string, args []any) (bool, error) {
	if dir == "" {
		return false, nil
	}
	var count int
	query := "SELECT count(*) FROM (SELECT c.id FROM conversations c WHERE " + filter + " LIMIT ?)"
	values := append(append([]any{}, args...), directoryScanThreshold+1)
	if err := index.conn.QueryRowContext(ctx, query, values...).Scan(&count); err != nil {
		return false, index.contention(fmt.Errorf("select indexed directories: %w", err))
	}
	return count <= directoryScanThreshold, nil
}

func (index *historyIndex) matches(ctx context.Context, s *search, file indexedFile, changed bool) (err error) {
	if changed {
		if err := index.refreshPlans(ctx, s, file.id); err != nil {
			return err
		}
	}
	if !changed && !index.candidates[file.id] {
		return nil
	}
	rows, err := index.queries.conversations.QueryContext(ctx, file.id)
	if err != nil {
		return index.contention(fmt.Errorf("query indexed conversations: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	return index.collectMatches(ctx, s, rows)
}

func (index *historyIndex) collectMatches(ctx context.Context, s *search, rows *sql.Rows) error {
	for rows.Next() {
		var id int64
		var metadata string
		if err := rows.Scan(&id, &metadata); err != nil {
			return fmt.Errorf("read indexed conversation: %w", err)
		}
		var conversation Conversation
		if err := json.Unmarshal([]byte(metadata), &conversation); err != nil {
			return fmt.Errorf("decode indexed conversation: %w", err)
		}
		if !s.accepts(conversation) {
			continue
		}
		if index.plans[id].count == 0 {
			continue
		}
		var match Match
		match.Conversation = conversation
		if err := index.readExcerpts(ctx, s, id, &match); err != nil {
			return err
		}
		s.add(ctx, match)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan indexed conversations: %w", err)
	}
	return nil
}

func (index *historyIndex) readExcerpts(ctx context.Context, s *search, id int64, match *Match) (err error) {
	var t transcript
	t.match = *match
	plan := index.plans[id]
	rows, err := index.queries.parts.QueryContext(ctx, plan.ids[0], plan.ids[1], plan.ids[2])
	if err != nil {
		return index.contention(fmt.Errorf("query indexed text: %w", err))
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var excerpt Excerpt
		var timestamp string
		if err = rows.Scan(&excerpt.Role, &excerpt.Text, &excerpt.MessageID, &excerpt.Line, &timestamp); err != nil {
			return fmt.Errorf("read indexed text: %w", err)
		}
		s.matchText(&t, excerpt.Role, excerpt.Text, excerpt.MessageID, excerpt.Line, nativeTime(timestamp))
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("scan indexed text: %w", err)
	}
	t.match.MatchingParts = plan.count
	*match = t.match
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
