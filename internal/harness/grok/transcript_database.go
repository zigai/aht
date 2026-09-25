package grok

import (
	"context"
	"database/sql"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

const nativeQuery = `SELECT s.id AS session_id, s.title AS title, s.cwd_last AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.seq AS TEXT) AS message_id,
 m.role AS role, m.message_json AS body, m.created_at AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	return nativeQuery, transcript.MessageRow, nil
}
