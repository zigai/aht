package history

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	native "github.com/zigai/aht/v2/internal/harness/transcript"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	boundedSQLPrefix = `SELECT session_id, COALESCE(title,''), COALESCE(cwd,''),
 CAST(created AS TEXT), CAST(updated AS TEXT), message_id, COALESCE(role,''),
 CASE WHEN length(CAST(COALESCE(body,'') AS BLOB))<=? THEN COALESCE(body,'') END, CAST(timestamp AS TEXT)
 FROM (`
	boundedSQLSuffix = `) ORDER BY session_id, timestamp, message_order`
)

type databaseRow = native.Row

func (s *parser) scanDatabase(
	ctx context.Context,
	source Source,
	path string,
	buffer *fileIssueBuffer,
) {
	db, err := native.OpenDatabase(path)
	if err != nil {
		buffer.issue(source, path, fmt.Errorf("open history database: %w", err))
		return
	}

	defer func() {
		if err := db.Close(); err != nil {
			buffer.issue(source, path, err)
		}
	}()

	query, format, err := historySQL(ctx, db, source.Harness)
	if err != nil {
		buffer.issue(source, path, err)
		return
	}

	rows, err := db.QueryContext(ctx, query, maxRecordBytes)
	if err != nil {
		buffer.issue(source, path, fmt.Errorf("read history database schema: %w", err))
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			buffer.issue(source, path, err)
		}
	}()

	s.databaseRows(ctx, source, path, format, rows, buffer)
}

func historySQL(
	ctx context.Context,
	db *sql.DB,
	h registry.Harness,
) (string, native.RowReader, error) {
	reader := catalog.TranscriptFor(h)
	if reader.Query == nil {
		return "", nil, errUnknownFormat
	}

	query, parse, err := reader.Query(ctx, db)
	if err != nil {
		return "", nil, fmt.Errorf("prepare history query: %w", err)
	}

	return boundedSQLPrefix + query + boundedSQLSuffix, parse, nil
}

func (s *parser) databaseRows(
	ctx context.Context,
	source Source,
	path string,
	parse native.RowReader,
	rows *sql.Rows,
	buffer *fileIssueBuffer,
) {
	var t transcript
	defer func() {
		buffer.selectConversation(
			t.match.Conversation,
			s.selected(&t),
		)
		s.sink.conversation(ctx, &t)
	}()

	for rows.Next() {
		if ctx.Err() != nil {
			return
		}

		var row databaseRow
		if err := rows.Scan(
			&row.SessionID,
			&row.Title,
			&row.CWD,
			&row.Created,
			&row.Updated,
			&row.MessageID,
			&row.Role,
			&row.Body,
			&row.Timestamp,
		); err != nil {
			buffer.issue(source, path, fmt.Errorf("read history row: %w", err))
			return
		}

		if row.SessionID != t.match.Conversation.SessionID {
			buffer.selectConversation(
				t.match.Conversation,
				s.selected(&t),
			)
			s.sink.conversation(ctx, &t)
			t = newDatabaseTranscript(
				Conversation{
					Harness:     source.Harness,
					SessionID:   row.SessionID,
					Path:        path,
					Title:       row.Title,
					CustomTitle: "",
					AITitle:     "",
					Prompt:      "",
					CWD:         row.CWD,
					ProjectRoot: "",
					GitBranch:   "",
					Model:       "",
					Messages:    0,
					CreatedAt:   native.StoredTime(row.Created),
					UpdatedAt:   native.StoredTime(row.Updated),
				},
			)
		}

		if !row.Body.Valid {
			buffer.recordProblem(source, path, errRecordSize)
			continue
		}

		if err := parse(ctx, s.decoder(source, &t, buffer), row); err != nil {
			buffer.issue(source, path, err)
		}
	}

	if err := rows.Err(); err != nil {
		buffer.issue(source, path, fmt.Errorf("scan history database: %w", err))
	}
}
