package transcript

import (
	"context"
	"database/sql"
	"fmt"
)

const partV1Query = `SELECT s.id AS session_id, s.title AS title, s.directory AS cwd,
 s.time_created AS created, s.time_updated AS updated, p.id AS message_id,
 json_extract(m.data,'$.role') AS role, p.data AS body, p.time_created AS timestamp
 FROM session s JOIN message m ON m.session_id=s.id JOIN part p ON p.message_id=m.id`

const partV2Query = `SELECT s.id AS session_id, s.title AS title, s.directory AS cwd,
 s.time_created AS created, s.time_updated AS updated, m.id AS message_id,
 m.type AS role, m.data AS body, m.time_created AS timestamp
 FROM session s JOIN session_message m ON m.session_id=s.id`

func PartDatabaseQuery(ctx context.Context, db *sql.DB) (string, RowReader, error) {
	var current bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='session_message')").Scan(&current); err != nil {
		return "", nil, fmt.Errorf("inspect history schema: %w", err)
	}
	if current {
		return partV2Query, partV2Row, nil
	}
	return partV1Query, partV1Row, nil
}

func partV1Row(ctx context.Context, t *Decoder, row Row) error {
	r, err := RowRecord(row)
	if err != nil {
		return err
	}
	switch Str(r, "type") {
	case "text":
		if row.Role == "user" || row.Role == "assistant" {
			t.Capture(ctx, row.Role, Str(r, "text"), row.MessageID, 0, StoredTime(row.Timestamp))
		}
	case "tool":
		t.Capture(ctx, "tool", ToolBlockText(r), row.MessageID, 0, StoredTime(row.Timestamp))
	}
	return nil
}

func partV2Row(ctx context.Context, t *Decoder, row Row) error {
	r, err := RowRecord(row)
	if err != nil {
		return err
	}
	if row.Role == "user" {
		t.Capture(ctx, "user", Str(r, "text"), row.MessageID, 0, StoredTime(row.Timestamp))
	}
	if row.Role == "assistant" {
		for _, part := range MessageParts(r["content"], "assistant", true) {
			t.Capture(ctx, part.Role, part.Text, row.MessageID, 0, StoredTime(row.Timestamp))
		}
	}
	return nil
}
