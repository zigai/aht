package goose

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

const transcriptQueryFormat = `SELECT s.id AS session_id, %s AS title, s.working_dir AS cwd,
 s.created_at AS created, s.updated_at AS updated,
 CAST(m.id AS TEXT) AS message_id, CAST(m.id AS TEXT) AS message_order,
 m.role AS role, m.content_json AS body, m.created_timestamp AS timestamp
 FROM sessions s JOIN messages m ON m.session_id=s.id`

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	columns, err := gooseSessionColumns(ctx, db)
	if err != nil {
		return "", nil, err
	}

	if !columns["id"] {
		return "", nil, errGooseSessionColumnsUnsupported
	}

	var titleExpr string

	switch {
	case columns["name"] && columns["description"]:
		titleExpr = "COALESCE(NULLIF(s.name, ''), s.description, '')"
	case columns["name"]:
		titleExpr = "COALESCE(s.name, '')"
	case columns["description"]:
		titleExpr = "COALESCE(s.description, '')"
	default:
		return "", nil, errGooseSessionColumnsUnsupported
	}

	return fmt.Sprintf(transcriptQueryFormat, titleExpr), transcript.BlocksRow, nil
}
