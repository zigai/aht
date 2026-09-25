package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestListRejectsModeSpecificFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	tests := [][]string{
		{"--store", path, "list", "--format", "plain"},
		{"--store", path, "list", "--no-snapshot"},
		{"--store", path, "list", "--watch", "--summary"},
		{"--store", path, "list", "--watch", "--summary=false"},
		{"--store", path, "list", "--watch", "--sort", "updated"},
		{"--store", path, "list", "--summary", "--desc"},
		{"--store", path, "list", "--summary", "--absolute-time"},
		{"--store", path, "--json", "list", "--absolute-time"},
		{"--store", path, "list", "--sort", ""},
		{"--store", path, "watch", "--format", ""},
	}
	for _, args := range tests {
		if err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Errorf("arguments unexpectedly accepted: %v", args)
		}
	}
}

func TestListDefaultsToLatestUpdateLastWithUsefulLabelsAndShortIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	old := observeTestSession(t, store, "older-session", time.Now().Add(-time.Hour))
	newer := observeTestSession(t, store, "newer-session", time.Now())

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertListTableOutput(t, stdout.String(), old, newer)

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"--store", path, "--json", "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertListJSONOutput(t, stdout.Bytes(), old.ID, newer.ID)
}

func assertListTableOutput(t *testing.T, output string, old, newer registry.Session) {
	t.Helper()
	oldIndex := strings.Index(output, "older-session")
	newIndex := strings.Index(output, "newer-session")
	if oldIndex < 0 || newIndex < 0 || oldIndex > newIndex {
		t.Fatalf("list does not put the latest update last (old=%d, new=%d):\n%s", oldIndex, newIndex, output)
	}
	oldShort := shortRegistryID(old.ID)
	newerShort := shortRegistryID(newer.ID)
	if !strings.Contains(output, "Session") || !strings.Contains(output, oldShort) || !strings.Contains(output, newerShort) {
		t.Fatalf("list did not use a label and abbreviated IDs:\n%s", output)
	}
	if strings.Contains(output, old.ID) || strings.Contains(output, newer.ID) {
		t.Fatalf("list included unabbreviated IDs:\n%s", output)
	}
}

func assertListJSONOutput(t *testing.T, data []byte, oldID, newerID string) {
	t.Helper()
	var sessions []registry.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatalf("unmarshal list JSON: %v", err)
	}
	if len(sessions) != 2 || sessions[0].ID != oldID || sessions[1].ID != newerID {
		t.Fatalf("unexpected list JSON sessions: %#v", sessions)
	}
}

func TestListDisplaysAndFiltersZellijLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	location := &registry.Location{
		Kind: registry.MultiplexerZellij, SessionName: "work", TabName: "agents", PaneID: "terminal_7",
	}
	if _, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "zellij-session"}, Evidence: &registry.Report{Event: "turn_complete", Location: location}}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list", "--multiplexer-session", "work"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, expected := range []string{"Location", "zellij", "work"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("list output missing %q:\n%s", expected, output)
		}
	}
}

func TestAbbreviatedRegistryIDsExpandCollidingPrefixes(t *testing.T) {
	t.Parallel()
	sessions := []registry.Session{{ID: "codex-12345678aaaa"}, {ID: "codex-12345678bbbb"}, {ID: "claude-12345678cccc"}}
	ids := abbreviatedRegistryIDs(sessions)
	if ids[sessions[0].ID] != "codex-12345678a" || ids[sessions[1].ID] != "codex-12345678b" {
		t.Fatalf("colliding IDs were not expanded: %#v", ids)
	}
	if ids[sessions[2].ID] != "claude-12345678" {
		t.Fatalf("different agent prefix was unnecessarily expanded: %#v", ids)
	}
}

func TestListTableColumnsExpandsSessionAndCWDWhenWidthAllows(t *testing.T) {
	t.Parallel()
	rows := [][]string{
		{"omp-5afa9c61", "omp", "Format watch command column alignment", "live", "running", "tmux:0:2:zsh:%1", "~/Projects/sample-project", "1s ago"},
		{"pi-ea2cacd9", "pi", "2026-08-27T20-22-44-492Z_01a044e3-a40c-77dc-8593-f0f6a3a7c42f", "live", "idle", "tmux:0:3:zsh:%2", "~/Projects/config", "1s ago"},
	}

	// In a wide terminal (e.g. 200 columns), SESSION and CWD should not be truncated.
	wideCols := listTableColumns(rows, 200)
	sessionCol := wideCols[2]
	cwdCol := wideCols[6]
	if sessionCol.width < len("2026-08-27T20-22-44-492Z_01a044e3-a40c-77dc-8593-f0f6a3a7c42f") {
		t.Fatalf("session width in wide terminal = %d, want >= 60", sessionCol.width)
	}
	if cwdCol.width < len("~/Projects/sample-project") {
		t.Fatalf("CWD width in wide terminal = %d, want >= 25", cwdCol.width)
	}

	// In standard 120 width, SESSION and CWD get dynamic proportioned widths instead of static 14/18.
	stdCols := listTableColumns(rows, 120)
	if stdCols[2].width < 25 {
		t.Fatalf("session width in 120 terminal = %d, want >= 25", stdCols[2].width)
	}
	if stdCols[6].width < 15 {
		t.Fatalf("CWD width in 120 terminal = %d, want >= 15", stdCols[6].width)
	}
}

func TestListFullFlagRendersCompleteValues(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := registry.NewJournal(path, catalog.Rules{})
	now := time.Now().UTC()
	live := registry.PresenceLive
	longSession := "Deploy new analytics dashboard to production cluster for quarterly report"
	longPath := "/home/zigai/Projects/very/deeply/nested/repository/path/with/lots/of/subdirectories"
	session, err := store.Observe(context.Background(), registry.Observation{Harness: registry.Harness("codex"), At: now, Subject: registry.ObservationIdentity{SessionID: longSession}, Evidence: &registry.Report{Event: "start", Claim: &live, Listing: &registry.Listing{CWD: longPath}}})
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--store", path, "list", "--full"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Contains(output, "…") {
		t.Fatalf("full output contained truncation ellipsis: %q", output)
	}
	for _, value := range []string{session.ID, "Deploy", "analytics", "dashboard", "production", "quarterly", "report", "subdirectories"} {
		if !strings.Contains(output, value) {
			t.Fatalf("full output missing %q: %q", value, output)
		}
	}
}

func TestListFullLayoutUsesTableOnlyWhenUsefulColumnsFit(t *testing.T) {
	t.Parallel()
	rows := [][]string{{
		"omp-8123f29b-full-identifier",
		"omp",
		"2026-08-29T07-13-53-424Z_01a04c5e-2510-7000-86b9-e9be6ca73e54.jsonl",
		"live",
		"idle",
		"tmux:sesh:5:zsh:%21",
		"~/Projects/omp-extensions",
		"4h ago",
	}}
	columns, fits := listFullTableColumns(rows, 120)
	if fits {
		t.Fatal("full table unexpectedly fit in 120 columns")
	}
	if columns[2].width < 24 || columns[6].width < 20 {
		t.Fatalf("full table used unreadable flexible widths: session=%d CWD=%d", columns[2].width, columns[6].width)
	}

	var stdout bytes.Buffer
	app := &application{stdout: &stdout}
	if err := app.writeStackedHumanRows(columns, rows); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if strings.Contains(output, "…") || !strings.Contains(output, "Session:") || !strings.Contains(output, rows[0][2]) {
		t.Fatalf("stacked full output lost data: %q", output)
	}

	wideColumns, wideFits := listFullTableColumns(rows, 200)
	if !wideFits {
		t.Fatal("full table did not fit in 200 columns")
	}
	if err := validateHumanColumns(wideColumns, 200); err != nil {
		t.Fatalf("wide full table columns invalid: %v", err)
	}
	if got := strings.Join(wrapHumanSession(rows[0][2], wideColumns[2].width), ""); got != rows[0][2] {
		t.Fatalf("semantic session wrapping lost data: got %q", got)
	}
}

func TestSessionDisplayLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session registry.Session
		want    string
	}{
		{
			name:    "explicit session id",
			session: registry.Session{ID: "omp-12345678", SessionID: "custom-thread-name", SessionPath: "/tmp/path.jsonl"},
			want:    "custom-thread-name",
		},
		{
			name:    "omp timestamped jsonl path extracts uuid",
			session: registry.Session{ID: "omp-12345678", SessionPath: "/home/zigai/.omp/agent/sessions/-Projects-aht/2026-08-29T10-11-12-300Z_01a04d00-7b2c-7000-8cff-61086b324bf2.jsonl"},
			want:    "01a04d00-7b2c-7000-8cff-61086b324bf2",
		},
		{
			name:    "plain filename fallback",
			session: registry.Session{ID: "omp-12345678", SessionPath: "/tmp/my-transcript.jsonl"},
			want:    "my-transcript.jsonl",
		},
		{
			name:    "pane id fallback",
			session: registry.Session{ID: "omp-12345678", Location: registry.Location{Kind: registry.MultiplexerTmux, PaneID: "%12"}},
			want:    "%12",
		},
		{
			name:    "process pid fallback",
			session: registry.Session{ID: "omp-12345678", Process: &registry.ProcessIdentity{PID: 42189}},
			want:    "pid:42189",
		},
		{
			name:    "short id fallback",
			session: registry.Session{ID: "omp-1234567890abcdef"},
			want:    "omp-12345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionDisplayLabel(tt.session); got != tt.want {
				t.Fatalf("sessionDisplayLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
