package history_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

func databaseFixture(t *testing.T, filename, ddl string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), filename)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL;"+ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func databasePath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var seq int
	var name, path string
	if err := db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeSQLiteReadersAndWAL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		harness  registry.Harness
		filename string
		ddl      string
	}{
		{"goose", registry.Harness("goose"), "sessions.db", `CREATE TABLE sessions(id,name,working_dir,created_at,updated_at); CREATE TABLE messages(id,session_id,role,content_json,created_timestamp);
INSERT INTO sessions VALUES('native','title','/project','2026-09-01 00:00:00','2026-09-01 00:01:00');
INSERT INTO messages VALUES(1,'native','user','[{"type":"text","text":"refresh token"}]',1);
INSERT INTO messages VALUES(2,'native','assistant','[{"type":"toolResponse","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"tool-only"}]}}}]',2);`},
		{"hermes", registry.Harness("hermes"), "state.db", `CREATE TABLE sessions(id,title,started_at,ended_at); CREATE TABLE messages(id,session_id,role,content,timestamp);
INSERT INTO sessions VALUES('native','title',1,2); INSERT INTO messages VALUES(1,'native','user','refresh token',1); INSERT INTO messages VALUES(2,'native','tool','tool-only',2);`},
		{"hermes-current", registry.Harness("hermes"), "state.db", `CREATE TABLE sessions(id,title,cwd,git_repo_root,started_at,ended_at); CREATE TABLE messages(id,session_id,role,content,tool_calls,codex_message_items,timestamp);
INSERT INTO sessions VALUES('native','title','/project','/project',1,2);
INSERT INTO messages VALUES(1,'native','assistant','','[{"function":{"name":"read","arguments":"tool-only"}}]','[{"type":"message","channel":"final","content":[{"type":"output_text","text":"refresh token"}]}]',1);`},

		{"grok", registry.Harness("grok"), "grok.db", `CREATE TABLE sessions(id,title,cwd_last,created_at,updated_at); CREATE TABLE messages(seq,session_id,role,message_json,created_at);
INSERT INTO sessions VALUES('native','title','/project',1,2);
INSERT INTO messages VALUES(1,'native','user','{"role":"user","content":"refresh token"}',1);
INSERT INTO messages VALUES(2,'native','tool','{"role":"tool","content":[{"type":"tool-result","output":{"type":"text","value":"tool-only"}}]}',2);`},
		{"opencode-v1", registry.Harness("opencode"), "opencode.db", `CREATE TABLE session(id,title,directory,time_created,time_updated); CREATE TABLE message(id,session_id,data); CREATE TABLE part(id,message_id,data,time_created);
INSERT INTO session VALUES('native','title','/project',1,2); INSERT INTO message VALUES('m1','native','{"role":"assistant"}');
INSERT INTO part VALUES('p1','m1','{"type":"text","text":"refresh token"}',1);
INSERT INTO part VALUES('p2','m1','{"type":"tool","tool":"read","state":{"output":"tool-only"}}',2);`},
		{"opencode-v2", registry.Harness("opencode"), "opencode.db", `CREATE TABLE session(id,title,directory,time_created,time_updated); CREATE TABLE session_message(id,session_id,type,data,time_created);
INSERT INTO session VALUES('native','title','/project',1,2);
INSERT INTO session_message VALUES('m1','native','user','{"text":"refresh token"}',1);
INSERT INTO session_message VALUES('m2','native','assistant','{"content":[{"type":"tool","name":"read","state":{"content":[{"type":"text","text":"tool-only"}]}}]}',2);`},
		{"kilo-v2", registry.Harness("kilo"), "kilo.db", `CREATE TABLE session(id,title,directory,time_created,time_updated); CREATE TABLE session_message(id,session_id,type,data,time_created);
INSERT INTO session VALUES('native','title','/project',1,2);
INSERT INTO session_message VALUES('m1','native','user','{"text":"refresh token"}',1);
INSERT INTO session_message VALUES('m2','native','assistant','{"content":[{"type":"tool","name":"read","state":{"content":[{"type":"text","text":"tool-only"}]}}]}',2);`},
		{"openclaw", registry.Harness("openclaw"), "openclaw-agent.sqlite", `CREATE TABLE session_windows(session_id,display_name,created_at,updated_at); CREATE TABLE transcript_events(session_id,seq,event_json,created_at);
INSERT INTO session_windows VALUES('native','title',1,2);
INSERT INTO transcript_events VALUES('native',1,'{"type":"session","id":"native","cwd":"/project"}',1);
INSERT INTO transcript_events VALUES('native',2,'{"type":"message","message":{"role":"user","content":"refresh token"}}',1);
INSERT INTO transcript_events VALUES('native',3,'{"type":"message","message":{"role":"toolResult","content":[{"type":"text","text":"tool-only"}]}}',2);`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := databaseFixture(t, tt.filename, tt.ddl)
			path := databasePath(t, db)
			assertDatabaseSearch(t, tt.harness, path)
		})
	}
}

func assertDatabaseSearch(t *testing.T, h registry.Harness, path string) {
	t.Helper()
	// Keep the writer open with committed WAL pages while searching.
	before, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: h, Path: filepath.Dir(path)}}}
	result, err := c.Search(t.Context(), history.Query{Text: "refresh token"})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.SessionID != "native" {
		t.Fatalf("search = %#v, %v", result, err)
	}
	result, err = c.Search(t.Context(), history.Query{Text: "tool-only"})
	if err != nil || len(result.Matches) != 0 {
		t.Fatalf("default tool exclusion = %#v, %v", result, err)
	}
	result, err = c.Search(t.Context(), history.Query{Text: "tool-only", IncludeTools: true})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Excerpts[0].Role != "tool" {
		t.Fatalf("tool search = %#v, %v", result, err)
	}
	after, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("search modified native WAL")
	}
}

func TestHermesNullableAndOversizedMessageBodies(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, extraRows, status string
		matchingParts, issues   int
		err                     error
	}{
		{name: "nullable", status: "searched", matchingParts: 1},
		{
			name: "oversized", status: "failed", matchingParts: 2, issues: 1, err: history.ErrIncomplete,
			extraRows: `INSERT INTO messages VALUES(3,'native','assistant',zeroblob(16*1024*1024+1),3);
INSERT INTO messages VALUES(4,'native','assistant','another refresh token',4);`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := databaseFixture(t, "state.db", `
CREATE TABLE sessions(id,title,started_at,ended_at);
CREATE TABLE messages(id,session_id,role,content,timestamp);
INSERT INTO sessions VALUES('native','title',1,4);
INSERT INTO messages VALUES(1,'native','assistant',NULL,1);

INSERT INTO messages VALUES(2,'native','user','refresh token',2);`+tt.extraRows)
			catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("hermes"), Path: databasePath(t, db)}}}
			result, err := catalog.Search(t.Context(), history.Query{Text: "refresh token"})
			if !errors.Is(err, tt.err) || len(result.Issues) != tt.issues || len(result.Matches) != 1 || result.Matches[0].MatchingParts != tt.matchingParts || result.Sources[0].Status != tt.status {
				t.Fatalf("content search = %#v, %v", result, err)
			}
			if tt.issues > 0 && !strings.Contains(result.Issues[0].Message, "exceeds 16 MiB") {
				t.Fatalf("oversized content issue = %#v", result.Issues[0])
			}
		})
	}
}

func TestGooseMalformedMessageReportsPartialResults(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, body string }{
		{"invalid JSON", `{broken private-content`},
		{"invalid shape", `{"unexpected":"private-content"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := databaseFixture(t, "sessions.db", `
CREATE TABLE sessions(id,name,working_dir,created_at,updated_at);
CREATE TABLE messages(id,session_id,role,content_json,created_timestamp);
INSERT INTO sessions VALUES('native','title','/project',1,3);
INSERT INTO messages VALUES(1,'native','user','[{"type":"text","text":"refresh token"}]',1);
INSERT INTO messages VALUES(3,'native','assistant','[{"type":"text","text":"another refresh token"}]',3);`)
			if _, err := db.ExecContext(t.Context(), `INSERT INTO messages VALUES(2,'native','assistant',?,2)`, tt.body); err != nil {
				t.Fatal(err)
			}
			catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: []history.Source{{Harness: registry.Harness("goose"), Path: databasePath(t, db)}}}
			result, err := catalog.Search(t.Context(), history.Query{Text: "refresh token"})
			if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 1 || result.Matches[0].MatchingParts != 2 || len(result.Issues) != 1 || result.Sources[0].Status != "failed" {
				t.Fatalf("malformed content search = %#v, %v", result, err)
			}
			if strings.Contains(result.Issues[0].Message, "private-content") {
				t.Fatal("diagnostic exposed malformed message content")
			}
		})
	}
}

// toolDatabase creates a Goose database whose tool row is newer than its
// searchable text, so tool search can only change excerpts, never metadata.
func toolDatabase(t *testing.T) []history.Source {
	t.Helper()
	db := databaseFixture(t, "sessions.db", `CREATE TABLE sessions(id,name,working_dir,created_at,updated_at);
CREATE TABLE messages(id,session_id,role,content_json,created_timestamp);
INSERT INTO sessions VALUES('native','title','/project','2026-09-01 00:00:00','2026-09-01 00:02:00');
INSERT INTO messages VALUES(1,'native','user','[{"type":"text","text":"refresh token"}]','2026-09-01 00:01:00');
INSERT INTO messages VALUES(2,'native','tool','[{"type":"text","text":"tool-only"}]','2026-09-01 00:03:00');`)
	return []history.Source{{Harness: registry.Harness("goose"), Path: databasePath(t, db)}}
}

func TestSQLiteToolRowsFeedMetadataWithoutToolSearch(t *testing.T) {
	t.Parallel()
	for _, mode := range searchModes(t, toolDatabase(t)) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			plain, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token"})
			requireMatches(t, plain, err, 1)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Text: "refresh token", IncludeTools: true})
			requireMatches(t, tools, err, 1)
			if diff := cmp.Diff(plain.Matches[0].Conversation, tools.Matches[0].Conversation); diff != "" {
				t.Fatalf("conversation metadata depends on IncludeTools (-plain +tools):\n%s", diff)
			}
			if got := plain.Matches[0].Conversation.UpdatedAt.UTC().Format(time.RFC3339); got != "2026-09-01T00:03:00Z" {
				t.Fatalf("conversation UpdatedAt = %s, want the tool row timestamp", got)
			}
		})
	}
}

func TestSQLiteToolRowsAreSearchableOnlyWithTools(t *testing.T) {
	t.Parallel()
	for _, mode := range searchModes(t, toolDatabase(t)) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			plain, err := mode.catalog.Search(t.Context(), history.Query{Text: "tool-only"})
			requireMatches(t, plain, err, 0)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Text: "tool-only", IncludeTools: true})
			requireMatches(t, tools, err, 1)
			if tools.Matches[0].Excerpts[0].Role != "tool" {
				t.Fatalf("tool row excerpt role = %q", tools.Matches[0].Excerpts[0].Role)
			}
		})
	}
}
