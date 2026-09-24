package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"github.com/zigai/aht/internal/harness/catalog"
	native "github.com/zigai/aht/internal/harness/transcript"

	_ "modernc.org/sqlite" // Registers the CGO-free reader for native harness databases.

	"github.com/zigai/aht/pkg/registry"
)

const (
	boundedSQLPrefix = `SELECT session_id, COALESCE(title,''), COALESCE(cwd,''), CAST(created AS TEXT), CAST(updated AS TEXT),
 message_id, COALESCE(role,''), CASE WHEN length(CAST(COALESCE(body,'') AS BLOB))<=? THEN COALESCE(body,'') END, CAST(timestamp AS TEXT)
 FROM (`
	boundedSQLSuffix = `) ORDER BY session_id, timestamp, message_id`
)

type databaseRow = native.Row

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

func historySQL(ctx context.Context, db *sql.DB, h registry.Harness) (string, native.RowReader, error) {
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

func (s *search) databaseRows(ctx context.Context, source Source, path string, parse native.RowReader, rows *sql.Rows) {
	var t transcript
	defer func() { s.add(ctx, t.match) }()
	for rows.Next() {
		if ctx.Err() != nil {
			return
		}
		var row databaseRow
		if err := rows.Scan(&row.SessionID, &row.Title, &row.CWD, &row.Created, &row.Updated, &row.MessageID, &row.Role, &row.Body, &row.Timestamp); err != nil {
			s.issue(source, path, fmt.Errorf("read history row: %w", err))
			return
		}
		if row.SessionID != t.match.Conversation.SessionID {
			s.add(ctx, t.match)
			t.match.Excerpts = nil
			t.match.MatchingParts = 0
			t.match.Live = nil
			t.recognized = true
			t.match.Conversation = Conversation{Harness: source.Harness, SessionID: row.SessionID, Path: path, Title: row.Title, CWD: row.CWD, ProjectRoot: "", CreatedAt: native.StoredTime(row.Created), UpdatedAt: native.StoredTime(row.Updated)}
		}
		if !row.Body.Valid {
			s.issue(source, path, errRecordSize)
			continue
		}
		if err := parse(ctx, s.decoder(source, &t), row); err != nil {
			s.issue(source, path, err)
		}
	}
	if err := rows.Err(); err != nil {
		s.issue(source, path, fmt.Errorf("scan history database: %w", err))
	}
}
