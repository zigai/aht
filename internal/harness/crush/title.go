package crush

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

type titleDatabase struct {
	db        *sql.DB
	statement *sql.Stmt
}

type titleDatabases map[string]*titleDatabase

func (crushHarness) SessionTitles(
	ctx context.Context,
	identities []registry.ObservationIdentity,
) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Crush session titles: %w", err)
	}

	if len(identities) == 0 {
		return titles, nil
	}

	path := projectsPath(harness.HomeDir())
	if path == "" {
		return titles, nil
	}

	projects, err := readProjects(path)
	if err != nil {
		return titles, err
	}

	databases := make(titleDatabases)
	defer databases.close()

	var failures []error

	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return titles, errors.Join(append(failures, err)...)
		}

		if identity.SessionID == "" {
			continue
		}

		var lookupFailures []error

		titles[i], lookupFailures = databases.title(
			ctx,
			identity.SessionID,
			titleCandidates(projects, identity.CWD),
		)
		failures = append(failures, lookupFailures...)
	}

	return titles, errors.Join(failures...)
}

func (databases titleDatabases) title(
	ctx context.Context,
	sessionID string,
	paths []string,
) (string, []error) {
	var failures []error

	for _, path := range paths {
		database, err := databases.open(ctx, path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}

		if database == nil {
			continue
		}

		var title string

		err = database.statement.QueryRowContext(ctx, sessionID).Scan(&title)
		if err == nil {
			return title, failures
		}

		if !errors.Is(err, sql.ErrNoRows) {
			failures = append(
				failures,
				fmt.Errorf("read Crush title for session %q: %w", sessionID, err),
			)
		}
	}

	return "", failures
}

func (databases titleDatabases) open(ctx context.Context, path string) (*titleDatabase, error) {
	if database, ok := databases[path]; ok {
		return database, nil
	}

	database, err := openTitleDatabase(ctx, path)
	databases[path] = database

	return database, err
}

func (databases titleDatabases) close() {
	for _, database := range databases {
		if database != nil {
			_ = database.statement.Close()
			_ = database.db.Close()
		}
	}
}

func titleCandidates(projects []crushProject, cwd string) []string {
	matching := make([]crushProject, 0, 1)

	others := make([]crushProject, 0, len(projects))
	for _, project := range projects {
		if cwd != "" && registry.PathsEqual(project.Path, cwd) {
			matching = append(matching, project)
		} else {
			others = append(others, project)
		}
	}

	return projectDatabases(slices.Concat(matching, others))
}

func openTitleDatabase(ctx context.Context, path string) (*titleDatabase, error) {
	db, err := transcript.OpenDatabase(path)
	if err != nil {
		return nil, fmt.Errorf("open Crush title database: %w", err)
	}

	statement, err := db.PrepareContext(ctx, "SELECT COALESCE(title, '') FROM sessions WHERE id=?")
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("prepare Crush title lookup in %s: %w", path, err)
	}

	return &titleDatabase{db: db, statement: statement}, nil
}
