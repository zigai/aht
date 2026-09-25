package opencode

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func transcriptQuery(ctx context.Context, db *sql.DB) (string, transcript.RowReader, error) {
	query, parse, err := transcript.PartDatabaseQuery(ctx, db)
	if err != nil {
		return "", nil, fmt.Errorf("prepare transcript query: %w", err)
	}
	return query, parse, nil
}
