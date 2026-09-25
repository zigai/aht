package hermes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/zigai/aht/v2/pkg/registry"
)

var errHermesDatabaseNotRegular = errors.New("hermes session database is not a regular file")

func (hermesHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Hermes session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	db, err := openHermesSessionDatabase()
	if errors.Is(err, os.ErrNotExist) {
		return titles, nil
	}
	if err != nil {
		return titles, err
	}
	defer func() { _ = db.Close() }()
	statement, err := db.PrepareContext(ctx, "SELECT COALESCE(title, '') FROM sessions WHERE id=?")
	if err != nil {
		return titles, fmt.Errorf("prepare Hermes title lookup: %w", err)
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
			failures = append(failures, fmt.Errorf("read Hermes title for session %q: %w", identity.SessionID, err))
		}
	}
	return titles, errors.Join(failures...)
}

func openHermesSessionDatabase() (*sql.DB, error) {
	databasePath := filepath.Join(hermesHome(), "state.db")
	info, err := os.Stat(databasePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect hermes session database: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Hermes session database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("hermes session database %q: %w", databasePath, errHermesDatabaseNotRegular)
	}
	var databaseURL url.URL
	databaseURL.Scheme = "file"
	databaseURL.Path = databasePath
	databaseURL.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(250)"
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return nil, fmt.Errorf("open Hermes session database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
