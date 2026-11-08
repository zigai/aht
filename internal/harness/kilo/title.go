package kilo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/command"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	kiloTitleTimeout               = 5 * time.Second
	maxKiloDatabasePathOutputBytes = 4 << 10
)

var errKiloMultipleDatabasesMatch = errors.New("multiple Kilo databases match session")

type kiloTitleLookup struct {
	titles    []string
	matched   []bool
	ambiguous []bool
	failures  []error
}

func (kiloHarness) SessionTitles(
	ctx context.Context,
	identities []registry.ObservationIdentity,
) ([]string, error) {
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
			lookup.failures = append(
				lookup.failures,
				fmt.Errorf("lookup Kilo session titles: %w", err),
			)

			return lookup.titles, errors.Join(lookup.failures...)
		}

		lookup.readDatabase(ctx, path, identities)
	}

	return lookup.titles, errors.Join(lookup.failures...)
}

func (lookup *kiloTitleLookup) readDatabase(
	ctx context.Context,
	path string,
	identities []registry.ObservationIdentity,
) {
	db, err := transcript.OpenDatabase(path)
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

func (lookup *kiloTitleLookup) readTitles(
	ctx context.Context,
	db *sql.DB,
	identities []registry.ObservationIdentity,
) {
	for i, identity := range identities {
		if identity.SessionID == "" || lookup.ambiguous[i] {
			continue
		}

		var title string

		err := db.QueryRowContext(ctx, "SELECT title FROM session WHERE id=?", identity.SessionID).
			Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			lookup.failures = append(
				lookup.failures,
				fmt.Errorf("query Kilo title for session %q: %w", identity.SessionID, err),
			)

			return
		}

		lookup.recordTitle(i, identity.SessionID, title)
	}
}

func (lookup *kiloTitleLookup) recordTitle(index int, sessionID string, title string) {
	if lookup.matched[index] && lookup.titles[index] != title {
		lookup.titles[index] = ""
		lookup.failures = append(
			lookup.failures,
			fmt.Errorf("%w %q", errKiloMultipleDatabasesMatch, sessionID),
		)
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
	paths := kiloPaths(harness.HomeDir())
	if paths.database == "" {
		return nil, nil
	}

	binary, err := exec.LookPath(kiloCommand)
	if err == nil {
		return kiloCommandDatabasePath(ctx, binary)
	}

	if ctx.Err() != nil {
		return nil, fmt.Errorf("resolve Kilo database path: %w", ctx.Err())
	}

	return []string{paths.database}, nil
}

func kiloCommandDatabasePath(ctx context.Context, binary string) ([]string, error) {
	output, err := command.RunWithLimits(
		ctx,
		kiloTitleTimeout,
		maxKiloDatabasePathOutputBytes,
		binary,
		nil,
		"db",
		"path",
	)
	if err != nil {
		return nil, fmt.Errorf("resolve Kilo database path: %w", err)
	}

	path := strings.TrimSpace(string(output))
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
