package kilo

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	kiloTitleTimeout               = 5 * time.Second
	maxKiloDatabasePathOutputBytes = 4 << 10
)

var (
	errKiloDatabaseNotRegular         = errors.New("kilo database is not a regular file")
	errKiloMultipleDatabasesMatch     = errors.New("multiple Kilo databases match session")
	errKiloDatabasePathOutputTooLarge = errors.New("kilo database path output exceeds 4 KiB")
)

type kiloDatabasePathOutput struct {
	bytes.Buffer

	exceeded bool
}

type kiloTitleLookup struct {
	titles    []string
	matched   []bool
	ambiguous []bool
	failures  []error
}

func (kiloHarness) SessionTitles(ctx context.Context, identities []registry.ObservationIdentity) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Kilo session titles: %w", err)
	}
	if len(identities) == 0 {
		return titles, nil
	}
	paths, err := kiloDatabasePaths(ctx)
	if err != nil {
		return titles, err
	}
	lookup := kiloTitleLookup{
		titles:    titles,
		matched:   make([]bool, len(identities)),
		ambiguous: make([]bool, len(identities)),
		failures:  []error{},
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			lookup.failures = append(lookup.failures, fmt.Errorf("lookup Kilo session titles: %w", err))
			return lookup.titles, errors.Join(lookup.failures...)
		}
		lookup.readDatabase(ctx, path, identities)
	}
	return lookup.titles, errors.Join(lookup.failures...)
}

func (lookup *kiloTitleLookup) readDatabase(ctx context.Context, path string, identities []registry.ObservationIdentity) {
	db, err := openKiloSessionDatabase(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		lookup.failures = append(lookup.failures, err)
		return
	}
	if db == nil {
		return
	}
	lookup.readTitles(ctx, db, identities)
	if err := db.Close(); err != nil {
		lookup.failures = append(lookup.failures, fmt.Errorf("close Kilo database: %w", err))
	}
}

func openKiloSessionDatabase(path string) (*sql.DB, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect Kilo database: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Kilo database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("kilo database %q: %w", path, errKiloDatabaseNotRegular)
	}
	var databaseURL url.URL
	databaseURL.Scheme = "file"
	databaseURL.Path = path
	databaseURL.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(250)"
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return nil, fmt.Errorf("open Kilo database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func (lookup *kiloTitleLookup) readTitles(ctx context.Context, db *sql.DB, identities []registry.ObservationIdentity) {
	for i, identity := range identities {
		if identity.SessionID == "" || lookup.ambiguous[i] {
			continue
		}
		var title string
		err := db.QueryRowContext(ctx, "SELECT title FROM session WHERE id=?", identity.SessionID).Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			lookup.failures = append(lookup.failures, fmt.Errorf("query Kilo title for session %q: %w", identity.SessionID, err))
			return
		}
		lookup.recordTitle(i, identity.SessionID, title)
	}
}

func (lookup *kiloTitleLookup) recordTitle(index int, sessionID, title string) {
	if lookup.matched[index] && lookup.titles[index] != title {
		lookup.titles[index] = ""
		lookup.failures = append(lookup.failures, fmt.Errorf("%w %q", errKiloMultipleDatabasesMatch, sessionID))
		lookup.matched[index] = false
		lookup.ambiguous[index] = true
		return
	}
	if !lookup.matched[index] {
		lookup.titles[index] = title
		lookup.matched[index] = true
	}
}

func kiloDatabasePaths(ctx context.Context) ([]string, error) {
	binary, err := exec.LookPath(kiloCommand)
	if err == nil {
		return kiloCommandDatabasePath(ctx, binary)
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("resolve Kilo database path: %w", ctx.Err())
	}
	return kiloFallbackDatabasePaths()
}

func kiloCommandDatabasePath(ctx context.Context, binary string) ([]string, error) {
	requestCtx, cancel := context.WithTimeout(ctx, kiloTitleTimeout)
	defer cancel()
	command := exec.CommandContext(requestCtx, binary, "db", "path")
	var output kiloDatabasePathOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("resolve Kilo database path: %w", ctx.Err())
		}
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("resolve Kilo database path: %w", requestCtx.Err())
		}
		if output.exceeded {
			return nil, errKiloDatabasePathOutputTooLarge
		}
		return nil, fmt.Errorf("resolve Kilo database path: %w", err)
	}
	path := strings.TrimSpace(output.String())
	if path == "" || path == ":memory:" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve Kilo database path: %w", err)
		}
	}
	return []string{path}, nil
}

func kiloFallbackDatabasePaths() ([]string, error) {
	if path := strings.TrimSpace(os.Getenv("KILO_DB")); path != "" {
		if path == ":memory:" {
			return nil, nil
		}
		if filepath.IsAbs(path) {
			return []string{path}, nil
		}
		return []string{filepath.Join(kiloDataDir(), path)}, nil
	}
	return []string{filepath.Join(kiloDataDir(), "kilo.db")}, nil
}

func (output *kiloDatabasePathOutput) Write(data []byte) (int, error) {
	if len(data) > maxKiloDatabasePathOutputBytes-output.Len() {
		output.exceeded = true
		return 0, errKiloDatabasePathOutputTooLarge
	}
	n, err := output.Buffer.Write(data)
	if err != nil {
		return n, fmt.Errorf("buffer Kilo database path output: %w", err)
	}
	return n, nil
}

func kiloDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "kilo")
}
