package transcript

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"

	_ "modernc.org/sqlite" // Registers the CGO-free reader for native harness databases.
)

var errDatabaseNotRegular = errors.New("native database is not a regular file")

// OpenDatabase opens a native SQLite database read-only with one connection.
// A missing database returns an error that wraps [os.ErrNotExist].
func OpenDatabase(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect native database: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q", errDatabaseNotRegular, path)
	}

	// mode=ro prevents creation, migration and writes. Do not use immutable=1:
	// an actively running harness may have committed history in its WAL.
	var uri url.URL

	uri.Scheme = "file"
	uri.Path = path
	uri.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(250)"

	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("open native database %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	return db, nil
}
