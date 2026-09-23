package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Registers the CGO-free reader for native harness databases.

	"github.com/zigai/aht/pkg/registry"
)

const (
	boundedSQLPrefix = `SELECT session_id, COALESCE(title,''), COALESCE(cwd,''), CAST(created AS TEXT), CAST(updated AS TEXT),
 message_id, COALESCE(role,''), CASE WHEN length(CAST(COALESCE(body,'') AS BLOB))<=? THEN COALESCE(body,'') END, CAST(timestamp AS TEXT)
 FROM (`
	boundedSQLSuffix = `) ORDER BY session_id, timestamp, message_id`

	gooseQuery = `SELECT s.id AS session_id, s.name AS title, s.working_dir AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, m.content_json AS body, m.created_timestamp AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`
	hermesQuery = `SELECT s.id AS session_id, s.title AS title, '' AS cwd,
 s.started_at AS created, COALESCE(s.ended_at,s.started_at) AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, m.content AS body, m.timestamp AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`
	hermesCurrentQuery = `SELECT s.id AS session_id, s.title AS title, s.cwd AS cwd,
 s.started_at AS created, COALESCE(s.ended_at,s.started_at) AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, json_object('content',m.content,'tool_calls',m.tool_calls,'codex_message_items',m.codex_message_items,'project_root',s.git_repo_root) AS body,
 m.timestamp AS timestamp FROM sessions s JOIN messages m ON m.session_id=s.id`

	grokQuery = `SELECT s.id AS session_id, s.title AS title, s.cwd_last AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.seq AS TEXT) AS message_id,
 m.role AS role, m.message_json AS body, m.created_at AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`
	opencodeV1Query = `SELECT s.id AS session_id, s.title AS title, s.directory AS cwd,
 s.time_created AS created, s.time_updated AS updated, p.id AS message_id,
 json_extract(m.data,'$.role') AS role, p.data AS body, p.time_created AS timestamp
 FROM session s JOIN message m ON m.session_id=s.id JOIN part p ON p.message_id=m.id`
	opencodeV2Query = `SELECT s.id AS session_id, s.title AS title, s.directory AS cwd,
 s.time_created AS created, s.time_updated AS updated, m.id AS message_id,
 m.type AS role, m.data AS body, m.time_created AS timestamp
 FROM session s JOIN session_message m ON m.session_id=s.id`
	openclawQuery = `SELECT s.session_id AS session_id, s.display_name AS title, '' AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.seq AS TEXT) AS message_id,
 '' AS role, m.event_json AS body, m.created_at AS timestamp
 FROM session_windows s JOIN transcript_events m ON m.session_id=s.session_id`
)

type databaseRow struct {
	sessionID, title, cwd, created, updated, messageID, role string
	body                                                     sql.NullString
	timestamp                                                string
}

func (s *search) scanDatabase(ctx context.Context, source Source, path string) {
	if s.index != nil {
		// Refresh failures reach the caller as source issues and as the sticky
		// error index.commit reports; only an unusable index must be retained
		// here so the search can fall back to a direct scan.
		if err := s.index.database(ctx, s, source, path); errors.Is(err, errIndexUnavailable) {
			s.indexErr = err
		}
		return
	}
	// mode=ro prevents creation, migration and writes. Do not use immutable=1:
	// an actively running harness may have committed history in its WAL.
	var uri url.URL
	uri.Scheme = "file"
	uri.Path = path
	uri.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(250)"
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		s.issue(source, path, fmt.Errorf("open history database: %w", err))
		return
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if err := db.Close(); err != nil {
			s.issue(source, path, err)
		}
	}()
	query, format, err := historySQL(ctx, db, source.Harness)
	if err != nil {
		s.issue(source, path, err)
		return
	}
	rows, err := db.QueryContext(ctx, query, maxRecordBytes)
	if err != nil {
		s.issue(source, path, fmt.Errorf("read history database schema: %w", err))
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			s.issue(source, path, err)
		}
	}()
	s.databaseRows(ctx, source, path, format, rows)
}

func historySQL(ctx context.Context, db *sql.DB, h registry.Harness) (string, string, error) {
	switch h {
	case registry.HarnessGoose:
		return boundedSQLPrefix + gooseQuery + boundedSQLSuffix, "blocks", nil
	case registry.HarnessHermes:
		var current bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('sessions') WHERE name='git_repo_root') AND EXISTS(SELECT 1 FROM pragma_table_info('messages') WHERE name='codex_message_items')`).Scan(&current)
		if err != nil {
			return "", "", fmt.Errorf("inspect Hermes schema: %w", err)
		}
		if current {
			return boundedSQLPrefix + hermesCurrentQuery + boundedSQLSuffix, "hermes", nil
		}
		return boundedSQLPrefix + hermesQuery + boundedSQLSuffix, "text", nil
	case registry.HarnessGrok:
		return boundedSQLPrefix + grokQuery + boundedSQLSuffix, "message", nil
	case registry.HarnessOpenClaw:
		return boundedSQLPrefix + openclawQuery + boundedSQLSuffix, "tree", nil
	case registry.HarnessOpenCode, registry.HarnessKilo:
		var current bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='session_message')").Scan(&current); err != nil {
			return "", "", fmt.Errorf("inspect history schema: %w", err)
		}
		if current {
			return boundedSQLPrefix + opencodeV2Query + boundedSQLSuffix, "opencode-v2", nil
		}
		return boundedSQLPrefix + opencodeV1Query + boundedSQLSuffix, "opencode-v1", nil
	case registry.HarnessClaude, registry.HarnessCodex, registry.HarnessCursor, registry.HarnessCopilot, registry.HarnessCline, registry.HarnessKimiCode, registry.HarnessPi, registry.HarnessOmp, registry.HarnessAgy, registry.HarnessDroid, registry.HarnessAmp:
		return "", "", errUnknownFormat
	default:
		return "", "", errUnknownFormat
	}
}

func (s *search) databaseRows(ctx context.Context, source Source, path, format string, rows *sql.Rows) {
	var t transcript
	defer func() { s.add(ctx, t.match) }()
	for rows.Next() {
		if ctx.Err() != nil {
			return
		}
		var row databaseRow
		if err := rows.Scan(&row.sessionID, &row.title, &row.cwd, &row.created, &row.updated, &row.messageID, &row.role, &row.body, &row.timestamp); err != nil {
			s.issue(source, path, fmt.Errorf("read history row: %w", err))
			return
		}
		if row.sessionID != t.match.Conversation.SessionID {
			s.add(ctx, t.match)
			t.match.Excerpts = nil
			t.match.MatchingParts = 0
			t.match.Live = nil
			t.recognized = true
			t.match.Conversation = Conversation{Harness: source.Harness, SessionID: row.sessionID, Path: path, Title: row.title, CWD: row.cwd, ProjectRoot: "", CreatedAt: storedTime(row.created), UpdatedAt: storedTime(row.updated)}
		}
		if !row.body.Valid {
			s.issue(source, path, errRecordSize)
			continue
		}
		s.databaseMessage(ctx, &t, row, format, source, path)
	}
	if err := rows.Err(); err != nil {
		s.issue(source, path, fmt.Errorf("scan history database: %w", err))
	}
}

func (s *search) databaseMessage(ctx context.Context, t *transcript, row databaseRow, format string, source Source, path string) {
	timestamp := storedTime(row.timestamp)
	if format == "text" {
		if role, ok := recognizedRole(row.role); ok {
			s.capture(ctx, t, role, row.body.String, row.messageID, 0, timestamp)
		}
		return
	}
	if format == "blocks" {
		var blocks []record
		if err := json.Unmarshal([]byte(row.body.String), &blocks); err != nil {
			s.issue(source, path, errInvalidRecord)
			return
		}
		role, ok := recognizedRole(row.role)
		if !ok {
			return
		}
		for _, part := range messageBlockParts(blocks, role, true) {
			s.capture(ctx, t, part.role, part.text, row.messageID, 0, timestamp)
		}
		return
	}
	var r record
	if err := json.Unmarshal([]byte(row.body.String), &r); err != nil {
		s.issue(source, path, errInvalidRecord)
		return
	}
	s.databaseRecord(ctx, t, row, format, r)
}

func (s *search) databaseRecord(ctx context.Context, t *transcript, row databaseRow, format string, r record) {
	switch format {
	case "message":
		s.message(ctx, t, r, row.messageID, 0, storedTime(row.timestamp))
	case "hermes":
		s.hermesMessage(ctx, t, row, r)
	case "tree":
		s.databaseTree(ctx, t, row, r)
	case "opencode-v1":
		s.opencodePart(ctx, t, r, row.role, row.messageID, storedTime(row.timestamp))
	case "opencode-v2":
		if row.role == "user" {
			s.capture(ctx, t, "user", str(r, "text"), row.messageID, 0, storedTime(row.timestamp))
		}
		if row.role == "assistant" {
			for _, part := range messageParts(r["content"], "assistant", true) {
				s.capture(ctx, t, part.role, part.text, row.messageID, 0, storedTime(row.timestamp))
			}
		}
	}
}

func storedTime(value string) time.Time {
	if strings.ContainsAny(value, "-T:") {
		return nativeTime(value)
	}
	return parseTime(json.RawMessage(value))
}

func (s *search) opencodePart(ctx context.Context, t *transcript, r record, role, id string, timestamp time.Time) {
	switch str(r, "type") {
	case "text":
		if role == "user" || role == "assistant" {
			s.capture(ctx, t, role, str(r, "text"), id, 0, timestamp)
		}
	case "tool":
		s.capture(ctx, t, "tool", toolBlockText(r), id, 0, timestamp)
	}
}

func toolBlockText(r record) string {
	state := obj(r, "state")
	return firstString(r, "tool", "name") + " " + string(state["input"]) + " " + str(state, "output") + " " + contentText(state["content"]) + " " + str(state, "error") + " " + str(obj(state, "error"), "message")
}

func (s *search) databaseTree(ctx context.Context, t *transcript, row databaseRow, r record) {
	if str(r, "type") != "message" {
		s.treeRecord(ctx, t, r, 0)
		return
	}
	timestamp := parseTime(r["timestamp"])
	if timestamp.IsZero() {
		timestamp = storedTime(row.timestamp)
	}
	id := str(r, "id")
	if id == "" {
		id = row.messageID
	}
	s.message(ctx, t, obj(r, "message"), id, 0, timestamp)
}

func (s *search) hermesMessage(ctx context.Context, t *transcript, row databaseRow, r record) {
	t.match.Conversation.ProjectRoot = str(r, "project_root")
	role, ok := recognizedRole(row.role)
	if !ok {
		return
	}
	timestamp := storedTime(row.timestamp)
	body := str(r, "content")
	if body == "" && role == "assistant" {
		body = hermesReply(str(r, "codex_message_items"))
	}
	s.capture(ctx, t, role, body, row.messageID, 0, timestamp)
	s.capture(ctx, t, "tool", str(r, "tool_calls"), row.messageID, 0, timestamp)
}

func hermesReply(encoded string) string {
	var items []record
	if json.Unmarshal([]byte(encoded), &items) != nil {
		return ""
	}
	var text strings.Builder
	for _, item := range items {
		if str(item, "type") != "message" {
			continue
		}
		channel := str(item, "channel")
		if channel != "" && channel != "final" {
			continue
		}
		text.WriteString(contentText(item["content"]))
	}
	return text.String()
}
