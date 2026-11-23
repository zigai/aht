package agy_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/agy"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionTitlesReadConversationSummaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(
		t.Context(),
		"CREATE TABLE conversation_summaries(conversation_id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '')",
	); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(
		t.Context(),
		"INSERT INTO conversation_summaries VALUES('titled', ' Review the parser '), ('untitled', '')",
	); err != nil {
		t.Fatal(err)
	}

	titles, err := agy.New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "untitled"},
		{SessionID: "titled"},
		{SessionID: "unknown"},
		{SessionID: ""},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"", "Review the parser", "", ""}; !slices.Equal(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
}

func TestSessionTitlesWithoutConversationSummaries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	titles, err := agy.New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "titled"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{""}; !slices.Equal(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
}
