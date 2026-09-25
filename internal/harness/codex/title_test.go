package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionTitlesUsesLatestIndexNameAcrossSessionPaths(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	index := `{"id":"one","thread_name":"Old"}` + "\n" +
		`{"id":"unrelated","thread_name":"` + strings.Repeat("x", 65<<10) + `"}` + "\n" +
		`{"id":"two","thread_name":"Other"}` + "\n" +
		`{"id":"one","thread_name":"New"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "one", SessionPath: filepath.Join(home, "sessions", "2026", "rollout.jsonl")},
		{SessionID: "two", SessionPath: filepath.Join(home, "archived_sessions", "rollout.jsonl")},
		{SessionID: "unknown", SessionPath: filepath.Join(home, "sessions", "rollout.jsonl")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"New", "Other", ""}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionTitlesFollowsCodexStateNamePrecedence(t *testing.T) {
	home := t.TempDir()
	databaseHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("sqlite_home = "+strconv.Quote(databaseHome)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index := `{"id":"paginated","thread_name":"Old index name"}` + "\n" +
		`{"id":"unnamed","thread_name":"Stale index name"}` + "\n" +
		`{"id":"legacy-title","thread_name":"Older index name"}` + "\n" +
		`{"id":"legacy-index","thread_name":"Index fallback"}` + "\n" +
		`{"id":"index-only","thread_name":"Index only"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(databaseHome, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE threads (id TEXT PRIMARY KEY, history_mode TEXT, title TEXT, first_user_message TEXT, name TEXT)",
		`INSERT INTO threads VALUES ('paginated','paginated','Preview','Preview','Current state name')`,
		`INSERT INTO threads VALUES ('unnamed','paginated','Preview','Preview',NULL)`,
		`INSERT INTO threads VALUES ('legacy-title','legacy','Explicit title','Prompt','')`,
		`INSERT INTO threads VALUES ('legacy-index','legacy','Prompt','Prompt','')`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{"paginated", "unnamed", "legacy-title", "legacy-index", "index-only"}
	identities := make([]registry.ObservationIdentity, len(ids))
	for i, id := range ids {
		identities[i] = registry.ObservationIdentity{SessionID: id, SessionPath: filepath.Join(home, "sessions", "rollout.jsonl")}
	}
	titles, err := New().SessionTitles(t.Context(), identities)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Current state name", "", "Explicit title", "Index fallback", "Index only"}, titles); diff != "" {
		t.Fatalf("titles mismatch (-want +got):\n%s", diff)
	}
}

func TestSessionTitlesUsesCodexHomeWithoutTranscriptPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(`{"id":"native","thread_name":"Index name"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "native"}})
	if err != nil || len(titles) != 1 || titles[0] != "Index name" {
		t.Fatalf("Codex home title = %v, %v", titles, err)
	}
}
