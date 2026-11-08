package hermes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (hermesHarness) SessionTitles(
	ctx context.Context,
	identities []registry.ObservationIdentity,
) ([]string, error) {
	titles := make([]string, len(identities))
	if err := ctx.Err(); err != nil {
		return titles, fmt.Errorf("lookup Hermes session titles: %w", err)
	}

	if len(identities) == 0 {
		return titles, nil
	}

	home := hermesHome()
	if home == "" {
		return titles, nil
	}

	db, err := transcript.OpenDatabase(filepath.Join(home, "state.db"))
	if errors.Is(err, os.ErrNotExist) {
		return titles, nil
	}

	if err != nil {
		return titles, fmt.Errorf("open Hermes state database: %w", err)
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

		if err := statement.QueryRowContext(ctx, identity.SessionID).
			Scan(&titles[i]); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			failures = append(
				failures,
				fmt.Errorf("read Hermes title for session %q: %w", identity.SessionID, err),
			)
		}
	}

	return titles, errors.Join(failures...)
}
