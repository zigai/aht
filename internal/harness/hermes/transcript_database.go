package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zigai/aht/internal/harness/transcript"
)

const nativeQuery = `SELECT s.id AS session_id, s.title AS title, '' AS cwd,
 s.started_at AS created, COALESCE(s.ended_at,s.started_at) AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, m.content AS body, m.timestamp AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`

const currentQuery = `SELECT s.id AS session_id, s.title AS title, s.cwd AS cwd,
 s.started_at AS created, COALESCE(s.ended_at,s.started_at) AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, json_object('content',m.content,'tool_calls',m.tool_calls,'codex_message_items',m.codex_message_items,'project_root',s.git_repo_root) AS body,
 m.timestamp AS timestamp FROM sessions s JOIN messages m ON m.session_id=s.id`

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	var current bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('sessions') WHERE name='git_repo_root') AND EXISTS(SELECT 1 FROM pragma_table_info('messages') WHERE name='codex_message_items')`).Scan(&current)
	if err != nil {
		return "", nil, fmt.Errorf("inspect Hermes schema: %w", err)
	}
	if current {
		return currentQuery, readDatabaseRow, nil
	}
	return nativeQuery, transcript.TextRow, nil
}

func readDatabaseRow(ctx context.Context, t *transcript.Decoder, row transcript.Row) error {
	r, err := transcript.RowRecord(row)
	if err != nil {
		return fmt.Errorf("decode history row: %w", err)
	}
	t.Conversation.ProjectRoot = transcript.Str(r, "project_root")
	role, ok := transcript.RecognizedRole(row.Role)
	if !ok {
		return nil
	}
	timestamp := transcript.StoredTime(row.Timestamp)
	body := transcript.Str(r, "content")
	if body == "" && role == "assistant" {
		body = hermesReply(transcript.Str(r, "codex_message_items"))
	}
	t.Capture(ctx, role, body, row.MessageID, 0, timestamp)
	t.Capture(ctx, "tool", transcript.Str(r, "tool_calls"), row.MessageID, 0, timestamp)
	return nil
}

func hermesReply(encoded string) string {
	var items []transcript.Record
	if json.Unmarshal([]byte(encoded), &items) != nil {
		return ""
	}
	var text strings.Builder
	for _, item := range items {
		if transcript.Str(item, "type") != "message" {
			continue
		}
		channel := transcript.Str(item, "channel")
		if channel != "" && channel != "final" {
			continue
		}
		text.WriteString(transcript.ContentText(item["content"]))
	}
	return text.String()
}
