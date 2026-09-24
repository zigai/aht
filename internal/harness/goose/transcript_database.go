package goose

import (
	"context"
	"database/sql"

	"github.com/zigai/aht/internal/harness/transcript"
)

const nativeQuery = `SELECT s.id AS session_id, s.name AS title, s.working_dir AS cwd,
 s.created_at AS created, s.updated_at AS updated, CAST(m.id AS TEXT) AS message_id,
 m.role AS role, m.content_json AS body, m.created_timestamp AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	return nativeQuery, transcript.BlocksRow, nil
}
