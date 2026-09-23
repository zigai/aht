package goose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/zigai/aht/pkg/registry"
)

var errGooseSessionColumnsUnsupported = errors.New("goose session database has no supported ID and display-name columns")

func (gooseHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Goose session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	db, err := openGooseSessionDatabase()
	if errors.Is(err, os.ErrNotExist) {
		return titles, nil
	}
	if err != nil {
		return titles, err
	}
	defer func() { _ = db.Close() }()

	return readGooseSessionTitles(ctx, db, identities, titles)
}

func openGooseSessionDatabase() (*sql.DB, error) {
	path := gooseSessionDatabasePath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect Goose session database: %w", err)
	} else if err != nil {
		return nil, fmt.Errorf("inspect Goose session database: %w", err)
	}
	var databaseURL url.URL
	databaseURL.Scheme = "file"
	databaseURL.Path = path
	databaseURL.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(250)"
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return nil, fmt.Errorf("open Goose session database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func readGooseSessionTitles(
	ctx context.Context,
	db *sql.DB,
	identities []registry.ObservationIdentity,
	titles []string,
) ([]string, error) {
	columns, err := gooseSessionColumns(ctx, db)
	if err != nil {
		return titles, err
	}
	query, err := gooseSessionTitleQuery(columns)
	if err != nil {
		return titles, err
	}

	statement, err := db.PrepareContext(ctx, query)
	if err != nil {
		return titles, fmt.Errorf("prepare Goose title lookup: %w", err)
	}
	defer func() { _ = statement.Close() }()
	var failures []error
	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}
		if identity.SessionID == "" {
			continue
		}
		if err := statement.QueryRowContext(ctx, identity.SessionID).Scan(&titles[i]); err != nil && !errors.Is(err, sql.ErrNoRows) {
			failures = append(failures, fmt.Errorf("read Goose title for session %q: %w", identity.SessionID, err))
		}
	}
	return titles, errors.Join(failures...)
}

func gooseSessionTitleQuery(columns map[string]bool) (string, error) {
	if !columns["id"] {
		return "", errGooseSessionColumnsUnsupported
	}
	if columns["description"] {
		return "SELECT COALESCE(description,'') FROM sessions WHERE id=?", nil
	}
	if columns["name"] {
		return "SELECT COALESCE(name,'') FROM sessions WHERE id=?", nil
	}
	return "", errGooseSessionColumnsUnsupported
}

func gooseSessionColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(sessions)")
	if err != nil {
		return nil, fmt.Errorf("inspect Goose session table: %w", err)
	}
	defer func() { _ = rows.Close() }()
	columns := make(map[string]bool)
	for rows.Next() {
		var sequence int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&sequence, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read Goose session columns: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Goose session columns: %w", err)
	}
	return columns, nil
}

func gooseSessionDatabasePath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	gooseDir := filepath.Join(data, "goose")
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		gooseDir = filepath.Join(appData, "Block", "goose")
	}
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		gooseDir = filepath.Join(root, "data")
	}
	return filepath.Join(gooseDir, "sessions", "sessions.db")
}
