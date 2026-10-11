package history_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
		{"crush", registry.Harness("crush"), "crush.db", crushDDL + `
INSERT INTO sessions VALUES('native',NULL,'title',1,2);
INSERT INTO sessions VALUES('task-call',  'native','delegated task',1,2);
INSERT INTO sessions VALUES('title-native','native','Generate a title',1,2);
INSERT INTO messages VALUES('m1','native','user','[{"type":"text","data":{"text":"refresh token"}}]',NULL,1);
INSERT INTO messages VALUES('m2','native','assistant','[{"type":"reasoning","data":{"thinking":"reasoning-only"}},{"type":"tool_call","data":{"id":"c1","name":"view","input":"{}"}}]','model-a',1);
INSERT INTO messages VALUES('m3','native','tool','[{"type":"tool_result","data":{"tool_call_id":"c1","name":"view","content":"tool-only"}}]',NULL,2);
INSERT INTO messages VALUES('m4','task-call','user','[{"type":"text","data":{"text":"refresh token"}}]',NULL,1);
INSERT INTO messages VALUES('m5','title-native','user','[{"type":"text","data":{"text":"refresh token"}}]',NULL,1);`},
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

const crushDDL = `CREATE TABLE sessions(id TEXT PRIMARY KEY,parent_session_id TEXT,title TEXT,created_at INTEGER,updated_at INTEGER);
CREATE TABLE messages(id TEXT PRIMARY KEY,session_id TEXT,role TEXT,parts TEXT,model TEXT,created_at INTEGER);`

func TestCrushHistoryFindsRegisteredProjects(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CRUSH_GLOBAL_DATA", "")
	writeCrushProject(t, home, project, crushDDL+`
INSERT INTO sessions VALUES('native',NULL,'Token work',1,2);
INSERT INTO messages VALUES('m1','native','user','[{"type":"text","data":{"text":"refresh token"}}]',NULL,1);
INSERT INTO messages VALUES('m2','native','user','[{"type":"text","data":{"text":"hidden continuation","hidden":true}}]',NULL,2);`)

	c := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite"), Sources: nil, Progress: nil}
	result, err := c.Search(t.Context(), history.Query{Terms: []string{"refresh token"}, Harnesses: []registry.Harness{registry.Harness("crush")}})
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("search = %#v, %v", result, err)
	}
	conversation := result.Matches[0].Conversation
	if conversation.SessionID != "native" || conversation.Title != "Token work" || conversation.CWD != project {
		t.Fatalf("conversation = %#v, want native session in %s", conversation, project)
	}
	result, err = c.Search(t.Context(), history.Query{Terms: []string{"hidden continuation"}, Harnesses: []registry.Harness{registry.Harness("crush")}})
	if err != nil || len(result.Matches) != 0 {
		t.Fatalf("hidden continuation search = %#v, %v", result, err)
	}
}

func TestCrushHistoryPreservesMessageIdentityAndOrder(t *testing.T) {
	t.Parallel()
	db := databaseFixture(t, "crush.db", crushDDL+`
INSERT INTO sessions VALUES('native',NULL,'Session title',1,3);
INSERT INTO messages VALUES('z-first','native','user','[{"type":"text","data":{"text":"first token prompt"}}]',NULL,2);
INSERT INTO messages VALUES('a-next','native','user','[{"type":"text","data":{"text":"next token prompt"}}]',NULL,2);
INSERT INTO messages VALUES('earlier','native','assistant','[{"type":"text","data":{"text":"earlier token response"}}]','old-model',1);
INSERT INTO messages VALUES('latest','native','assistant','[{"type":"text","data":{"text":"latest token response"}}]','new-model',3);`)
	sources := []history.Source{{Harness: registry.Harness("crush"), Path: databasePath(t, db)}}
	for _, mode := range searchModes(t, sources) {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			result, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"token"}, Excerpts: 10})
			requireMatches(t, result, err, 1)
			match := result.Matches[0]
			if match.Conversation.Prompt != "first token prompt" || match.Conversation.Model != "new-model" {
				t.Fatalf("conversation = %#v, want first prompt and latest model", match.Conversation)
			}
			var ids []string
			for _, excerpt := range match.Excerpts {
				ids = append(ids, excerpt.MessageID)
			}
			if diff := cmp.Diff([]string{"earlier", "z-first", "a-next", "latest"}, ids); diff != "" {
				t.Fatalf("message IDs (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCrushHistoryReportsProjectDiscoveryErrors(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		directory  bool
		checkError func(error) bool
	}{
		{"missing", "", false, func(err error) bool { return err == nil }},
		{"malformed", `{"projects":`, false, func(err error) bool {
			_, ok := errors.AsType[*json.SyntaxError](err)
			return ok
		}},
		{"unreadable", "", true, func(err error) bool {
			_, ok := errors.AsType[*os.PathError](err)
			return ok
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_DATA_HOME", "")
			t.Setenv("CRUSH_GLOBAL_DATA", "")
			path := filepath.Join(home, ".local", "share", "crush", "projects.json")
			if test.content != "" {
				writeHistory(t, filepath.Dir(path), filepath.Base(path), test.content)
			}
			if test.directory {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			_, discoveryErr := history.DefaultSources()
			catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			_, searchErr := catalog.Search(t.Context(), history.Query{Terms: []string{"token"}})
			if !test.checkError(discoveryErr) || !test.checkError(searchErr) {
				t.Fatalf("%s registry errors: discovery = %v, search = %v, unexpected error type", test.name, discoveryErr, searchErr)
			}
			if discoveryErr != nil && !strings.Contains(discoveryErr.Error(), path) {
				t.Fatalf("registry error = %v, want path %s", discoveryErr, path)
			}
		})
	}
}

func writeCrushProject(t *testing.T, home string, project string, ddl string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(project, ".crush"), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(project, ".crush", "crush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	projects := `{"projects":[{"path":` + strconv.Quote(project) + `,"data_dir":".crush","last_accessed":"2026-09-01T00:00:00Z"}]}`
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "crush"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "share", "crush", "projects.json"), []byte(projects), 0o600); err != nil {
		t.Fatal(err)
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
	result, err := c.Search(t.Context(), history.Query{Terms: []string{"refresh token"}})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.SessionID != "native" {
		t.Fatalf("search = %#v, %v", result, err)
	}
	result, err = c.Search(t.Context(), history.Query{Terms: []string{"tool-only"}})
	if err != nil || len(result.Matches) != 0 {
		t.Fatalf("default tool exclusion = %#v, %v", result, err)
	}
	result, err = c.Search(t.Context(), history.Query{Terms: []string{"tool-only"}, IncludeTools: true})
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
			name: "oversized", status: "searched", matchingParts: 2, issues: 1, err: nil,
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
			result, err := catalog.Search(t.Context(), history.Query{Terms: []string{"refresh token"}})
			if !errors.Is(err, tt.err) || (tt.issues > 0 && !result.Issues[0].Record) || len(result.Issues) != tt.issues || len(result.Matches) != 1 || result.Matches[0].MatchingParts != tt.matchingParts || result.Sources[0].Status != tt.status {
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
			result, err := catalog.Search(t.Context(), history.Query{Terms: []string{"refresh token"}})
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
			plain, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"refresh token"}})
			requireMatches(t, plain, err, 1)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"refresh token"}, IncludeTools: true})
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
			plain, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"tool-only"}})
			requireMatches(t, plain, err, 0)
			tools, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"tool-only"}, IncludeTools: true})
			requireMatches(t, tools, err, 1)
			if tools.Matches[0].Excerpts[0].Role != "tool" {
				t.Fatalf("tool row excerpt role = %q", tools.Matches[0].Excerpts[0].Role)
			}
		})
	}
}
