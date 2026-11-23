package goose

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestMatchersOmitInvalidWildcard(t *testing.T) {
	hooks, ok := gooseHookConfig()["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected Goose hooks map")
	}

	for _, event := range []string{"PreToolUse", "PostToolUse", "BeforeShellExecution"} {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("expected one %s hook group, got %#v", event, hooks[event])
		}

		group, ok := groups[0].(map[string]any)
		if !ok {
			t.Fatalf("expected %s hook group object", event)
		}

		if _, ok := group["matcher"]; ok {
			t.Fatalf("expected %s matcher to be omitted", event)
		}
	}
}

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string

		payload    string
		wantID     string
		wantPath   string
		wantCWD    string
		wantRoot   string
		wantEvent  string
		wantAttr   string
		wantAttrKV string
	}{
		{
			name: "goose",

			payload: `{"event":"PreToolUse","session_id":"goose-session",` +
				`"working_dir":"/repo/goose","tool_name":"shell"}`,
			wantID:     "goose-session",
			wantPath:   "",
			wantCWD:    "/repo/goose",
			wantRoot:   "/repo/goose",
			wantEvent:  "PreToolUse",
			wantAttr:   "goose_tool_name",
			wantAttrKV: "shell",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}

			got, err := New().PayloadDefaults(payload)
			if err != nil {
				t.Fatalf("unexpected error from PayloadDefaults: %v", err)
			}

			if got.SessionID != test.wantID ||
				got.SessionPath != test.wantPath ||
				got.CWD != test.wantCWD ||
				got.ProjectRoot != test.wantRoot ||
				got.Event != test.wantEvent {
				t.Fatalf("unexpected defaults: %#v", got)
			}

			if got.Attributes[test.wantAttr] != test.wantAttrKV {
				t.Fatalf(
					"expected attribute %s=%q, got %#v",
					test.wantAttr,
					test.wantAttrKV,
					got.Attributes,
				)
			}
		})
	}
}

func TestPayloadCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string

		payload string
		want    bool
	}{
		{
			name: "goose accepts native hook payload",

			payload: `{"event":"Stop","session_id":"goose-session","working_dir":"/repo"}`,
			want:    true,
		},
		{
			name: "goose rejects payload without session id",

			payload: `{"event":"Stop","working_dir":"/repo"}`,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := New().PayloadCompatible(json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("goose"), variable: "GOOSE_PATH_ROOT"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, New())

				if !knownHome {
					t.Setenv("HOME", "")
					t.Setenv("USERPROFILE", "")
				}

				override := t.TempDir()

				value := override
				if test.value != nil {
					value = test.value(override)
				}

				t.Setenv(test.variable, value)

				locations := New().Locations("")

				config := testharness.LocationPaths(locations, harness.LocationKindConfig)
				if len(config) == 0 || slices.ContainsFunc(config, func(path string) bool {
					return !strings.HasPrefix(path, override+string(filepath.Separator)) &&
						!strings.HasPrefix(path, "/etc/")
				}) {
					t.Errorf("config locations with %s=%s = %v", test.variable, value, config)
				}
			})
		}
	}
}

func TestSessionTitlesIgnoreWorkingDirectoryWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		plant   func(t *testing.T, workDir string)
	}{
		{registry.Harness("goose"), func(t *testing.T, workDir string) {
			t.Helper()
			testharness.PlantDatabase(
				t,
				workDir,
				"CREATE TABLE sessions (id TEXT, description TEXT)",
				"INSERT INTO sessions VALUES ('abc', 'Decoy')",
				".local",
				"share",
				"goose",
				"sessions",
				"sessions.db",
			)
		}},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			workDir := testharness.WithoutHome(t, New())
			test.plant(t, workDir)

			reader := New()

			titles, err := reader.SessionTitles(
				t.Context(),
				[]registry.ObservationIdentity{{SessionID: "abc"}},
			)
			if err != nil || len(titles) != 1 || titles[0] != "" {
				t.Fatalf("SessionTitles without a home = %q, %v, want one empty title", titles, err)
			}
		})
	}
}

func TestGooseTranscriptQuerySupportsLegacyAndMixedTitleSchemas(t *testing.T) {
	t.Parallel()

	const (
		messagesSchema = `CREATE TABLE messages (id INTEGER, session_id TEXT, role TEXT, ` +
			`content_json TEXT, created_timestamp TEXT);`
		insertMessage = `INSERT INTO messages VALUES ` +
			`(1, 's1', 'user', '[{"type":"text","text":"hello"}]', '2026-01-01');`
	)

	tests := []struct {
		name      string
		schemaSQL string
		insertSQL string
		wantTitle string
	}{
		{
			name: "description only schema",
			schemaSQL: `CREATE TABLE sessions (id TEXT, description TEXT, working_dir TEXT, ` +
				`created_at TEXT, updated_at TEXT);` + messagesSchema,
			insertSQL: `INSERT INTO sessions VALUES ('s1', 'Legacy title', '/work', '2026-01-01', '2026-01-01');` +
				insertMessage,
			wantTitle: "Legacy title",
		},
		{
			name: "name and description schema falls back to description when name is empty",
			schemaSQL: `CREATE TABLE sessions (id TEXT, name TEXT, description TEXT, working_dir TEXT, ` +
				`created_at TEXT, updated_at TEXT);` + messagesSchema,
			insertSQL: `INSERT INTO sessions VALUES ` +
				`('s1', '', 'Fallback title', '/work', '2026-01-01', '2026-01-01');` +
				insertMessage,
			wantTitle: "Fallback title",
		},
		{
			name: "name and description schema prefers non-empty name",
			schemaSQL: `CREATE TABLE sessions (id TEXT, name TEXT, description TEXT, working_dir TEXT, ` +
				`created_at TEXT, updated_at TEXT);` + messagesSchema,
			insertSQL: `INSERT INTO sessions VALUES ` +
				`('s1', 'Named title', 'Fallback title', '/work', '2026-01-01', '2026-01-01');` +
				insertMessage,
			wantTitle: "Named title",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			testharness.PlantDatabase(t, dir, test.schemaSQL, test.insertSQL, "sessions.db")

			db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = db.Close() }()

			query, _, err := transcriptQuery(t.Context(), db)
			if err != nil {
				t.Fatalf("transcriptQuery returned error: %v", err)
			}

			var sessionID, title, cwd, created, updated, msgID, msgOrder, role, body, ts string
			if err := db.QueryRowContext(t.Context(), query).Scan(
				&sessionID, &title, &cwd, &created, &updated,
				&msgID, &msgOrder, &role, &body, &ts,
			); err != nil {
				t.Fatalf("executing transcript query %q: %v", query, err)
			}

			if title != test.wantTitle {
				t.Fatalf("title = %q, want %q", title, test.wantTitle)
			}
		})
	}
}

func TestGooseTranscriptQueryRejectsUnsupportedTitleSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testharness.PlantDatabase(
		t,
		dir,
		"CREATE TABLE sessions (id TEXT, title TEXT, working_dir TEXT, created_at TEXT, updated_at TEXT);",
		"INSERT INTO sessions VALUES ('s1', 'Unknown title column', '/work', '2026-01-01', '2026-01-01');",
		"sessions.db",
	)

	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = db.Close() }()

	if _, _, err := New().Transcript().
		Query(t.Context(), db); !errors.Is(
		err,
		errGooseSessionColumnsUnsupported,
	) {
		t.Fatalf("transcript query error = %v, want %v", err, errGooseSessionColumnsUnsupported)
	}
}
