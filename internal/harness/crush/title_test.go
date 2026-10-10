package crush

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionTitlesSearchRegisteredProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CRUSH_GLOBAL_DATA", "")
	first := t.TempDir()
	second := t.TempDir()
	removed := t.TempDir()
	writeTitleDatabase(t, filepath.Join(first, ".crush"), "first-session", "First title")
	writeTitleDatabase(t, filepath.Join(second, "state"), "second-session", "Second title")
	writeProjects(t, home, []crushProject{
		{Path: removed, DataDir: filepath.Join(removed, ".crush")},
		{Path: first, DataDir: ".crush"},
		{Path: second, DataDir: filepath.Join(second, "state")},
	})

	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "second-session", CWD: first},
		{SessionID: "first-session", CWD: first},
		{SessionID: "unknown-session", CWD: second},
		{SessionID: "", CWD: first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Second title", "First title", "", ""}; !slices.Equal(titles, want) {
		t.Fatalf("titles = %q, want %q", titles, want)
	}
}

func writeTitleDatabase(t *testing.T, dataDir string, sessionID string, title string) {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE sessions(id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO sessions VALUES(?, NULL, ?)", sessionID, title); err != nil {
		t.Fatal(err)
	}
}

func writeProjects(t *testing.T, home string, projects []crushProject) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "crush")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
