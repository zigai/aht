package hermes

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readSessionRows(t *testing.T, sessionColumns, sessionValues string) transcript.Conversation {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE sessions (id TEXT, title TEXT, cwd TEXT, started_at REAL, ended_at REAL, git_repo_root TEXT` + sessionColumns + `)`,
		`CREATE TABLE messages (id INTEGER, session_id TEXT, role TEXT, content TEXT, tool_calls TEXT, codex_message_items TEXT, timestamp REAL)`,
		`INSERT INTO sessions VALUES ('native', 'title', '/work', 1, 2, '/root'` + sessionValues + `)`,
		`INSERT INTO messages (id, session_id, role, content, timestamp) VALUES (1, 'native', 'user', 'hello', 1)`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	query, read, err := transcriptQuery(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var conversation transcript.Conversation
	var recognized bool
	decoder := &transcript.Decoder{Conversation: &conversation, Recognized: &recognized}
	for rows.Next() {
		var row transcript.Row
		var created, updated, messageOrder sql.NullString
		if err := rows.Scan(&row.SessionID, &row.Title, &row.CWD, &created, &updated, &row.MessageID, &messageOrder, &row.Role, &row.Body, &row.Timestamp); err != nil {
			t.Fatal(err)
		}
		if err := read(t.Context(), decoder, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestTranscriptQueryCapturesBranchAndModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, sessionColumns, sessionValues string
		branch, model                       string
	}{
		{"both", ", git_branch TEXT, model TEXT", ", 'main', 'model-a'", "main", "model-a"},
		{"model only", ", model TEXT", ", 'model-a'", "", "model-a"},
		{"neither", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conversation := readSessionRows(t, tt.sessionColumns, tt.sessionValues)
			if conversation.ProjectRoot != "/root" || conversation.GitBranch != tt.branch || conversation.Model != tt.model {
				t.Fatalf("conversation = %+v, want branch %q model %q", conversation, tt.branch, tt.model)
			}
		})
	}
}
