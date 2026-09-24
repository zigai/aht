package openclaw

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zigai/aht/internal/harness/transcript"
)

const nativeQuery = `SELECT s.session_id AS session_id, s.display_name AS title, '' AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.seq AS TEXT) AS message_id,
 '' AS role, m.event_json AS body, m.created_at AS timestamp
 FROM session_windows s JOIN transcript_events m ON m.session_id=s.session_id`

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	return nativeQuery, readDatabaseRow, nil
}

func readDatabaseRow(ctx context.Context, t *transcript.Decoder, row transcript.Row) error {
	r, err := transcript.RowRecord(row)
	if err != nil {
		return fmt.Errorf("decode history row: %w", err)
	}
	if transcript.Str(r, "type") != "message" {
		transcript.TreeRecord(ctx, t, r, 0)
		return nil
	}
	timestamp := transcript.ParseTime(r["timestamp"])
	if timestamp.IsZero() {
		timestamp = transcript.StoredTime(row.Timestamp)
	}
	id := transcript.Str(r, "id")
	if id == "" {
		id = row.MessageID
	}
	t.Message(ctx, transcript.Obj(r, "message"), id, 0, timestamp)
	return nil
}
