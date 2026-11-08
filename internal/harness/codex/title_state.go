package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

var errConfigTooLarge = errors.New("codex config exceeds 1 MiB")

func readStateTitles(
	ctx context.Context,
	home string,
	indices map[string][]int,
	titles []string,
) error {
	sqliteHome, err := titleSQLiteHome(home)
	if err != nil {
		return err
	}

	db, err := transcript.OpenDatabase(filepath.Join(sqliteHome, "state_5.sqlite"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("open Codex state database: %w", err)
	}

	defer func() { _ = db.Close() }()

	ids := make([]string, 0, len(indices))
	for id := range indices {
		ids = append(ids, id)
	}

	return queryStateTitles(ctx, db, ids, indices, titles)
}

func titleSQLiteHome(home string) (string, error) {
	file, err := os.Open(filepath.Join(home, "config.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("open Codex config: %w", err)
	}

	if err == nil {
		defer func() { _ = file.Close() }()

		body, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
		if err != nil {
			return "", fmt.Errorf("read Codex config: %w", err)
		}

		if len(body) > 1<<20 {
			return "", errConfigTooLarge
		}

		var config struct {
			SQLiteHome string `toml:"sqlite_home"`
		}
		if err := toml.Unmarshal(body, &config); err != nil {
			return "", fmt.Errorf("decode Codex config: %w", err)
		}

		if config.SQLiteHome != "" {
			return config.SQLiteHome, nil
		}
	}

	if filepath.Clean(home) == filepath.Clean(codexHome()) {
		if value := os.Getenv("CODEX_SQLITE_HOME"); value != "" {
			return value, nil
		}
	}

	return home, nil
}

func queryStateTitles(
	ctx context.Context,
	db *sql.DB,
	ids []string,
	indices map[string][]int,
	titles []string,
) error {
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode Codex state title IDs: %w", err)
	}

	const query = "SELECT id, history_mode, title, first_user_message, name " +
		"FROM threads WHERE id IN (SELECT value FROM json_each(?))"

	rows, err := db.QueryContext(ctx, query, string(idsJSON))
	if err != nil {
		return fmt.Errorf("query Codex state titles: %w", err)
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id                              string
			mode, title, firstMessage, name sql.NullString
		)
		if err := rows.Scan(&id, &mode, &title, &firstMessage, &name); err != nil {
			return fmt.Errorf("scan Codex state title: %w", err)
		}

		var resolved string

		switch mode.String {
		case "paginated":
			resolved = strings.TrimSpace(name.String)
		case "legacy", "":
			resolved = strings.TrimSpace(title.String)
			if resolved == "" || resolved == strings.TrimSpace(firstMessage.String) {
				continue // The index supplies the legacy fallback.
			}
		default:
			continue
		}

		for _, i := range indices[id] {
			titles[i] = resolved
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan Codex state titles: %w", err)
	}

	return nil
}
