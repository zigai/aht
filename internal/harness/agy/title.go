package agy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

const titleDatabaseName = "conversation_summaries.db"

func (agyHarness) SessionTitles(
	ctx context.Context,
	identities []registry.ObservationIdentity,
) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Antigravity session titles: %w", err)
	}

	if len(identities) == 0 {
		return titles, nil
	}

	dir := agyApplicationDir()
	if dir == "" {
		return titles, nil
	}

	db, err := transcript.OpenDatabase(filepath.Join(dir, titleDatabaseName))
	if errors.Is(err, os.ErrNotExist) {
		return titles, nil
	}

	if err != nil {
		return titles, fmt.Errorf("open Antigravity conversation summaries: %w", err)
	}

	defer func() { _ = db.Close() }()

	statement, err := db.PrepareContext(
		ctx,
		"SELECT title FROM conversation_summaries WHERE conversation_id=?",
	)
	if err != nil {
		return titles, fmt.Errorf("prepare Antigravity title lookup: %w", err)
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

		var title string

		err := statement.QueryRowContext(ctx, identity.SessionID).Scan(&title)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			failures = append(
				failures,
				fmt.Errorf("read Antigravity title for session %q: %w", identity.SessionID, err),
			)

			continue
		}

		titles[i] = strings.TrimSpace(title)
	}

	return titles, errors.Join(failures...)
}
