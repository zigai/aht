package history_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

func indexedFixture(t *testing.T) (history.Catalog, string) {
	t.Helper()
	root := t.TempDir()
	path := writeHistory(t, root, "session.jsonl", treeHistory)
	return history.Catalog{Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}, path
}

func requireIndexedMatches(t *testing.T, c history.Catalog, q history.Query, count int) history.Result {
	t.Helper()
	result, err := c.Search(t.Context(), q)
	if err != nil || len(result.Matches) != count {
		t.Fatalf("search %q: matches=%d want=%d issues=%#v error=%v", q.Terms, len(result.Matches), count, result.Issues, err)
	}
	return result
}

func appendHistory(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(body)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestIndexRefreshesAppendAndRewrite(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	appendHistory(t, path, `{"type":"message","id":"new","message":{"role":"user","content":"appended token"}}`+"\n")
	result := requireIndexedMatches(t, c, history.Query{Terms: []string{"appended token"}}, 1)
	if result.Matches[0].Excerpts[0].Line != 7 {
		t.Fatalf("appended line = %d", result.Matches[0].Excerpts[0].Line)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same inode, same length and restored mtime still invalidates via ctime.
	//nolint:gosec // G703: path belongs to the isolated t.TempDir fixture, not transcript input.
	if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "refresh", "changed")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"changed"}}, 1)
	// A prefix rewrite followed by growth must not qualify as an append.
	rewritten := strings.ReplaceAll(string(data), "Refresh", "Removed") + `{"type":"session_info","name":"rewritten"}` + "\n"
	//nolint:gosec // G703: path belongs to the isolated t.TempDir fixture, not transcript input.
	if err = os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"Removed"}}, 1)
}

func TestRefreshStatsDescribeCompletedWork(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	var updates []history.Progress
	catalog.Progress = func(progress history.Progress) { updates = append(updates, progress) }
	cold := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	requireRefreshStats(t, cold, history.RefreshStats{Checked: 1, Reparsed: 1, BytesParsed: int64(len(treeHistory)), Reasons: map[string]int{"new file": 1}})
	if len(cold.Reparsed) != 1 {
		t.Fatalf("cold files = %#v", cold.Reparsed)
	}
	if cold.Refresh[0].ParseTime <= 0 || cold.Refresh[0].StoreTime <= 0 {
		t.Fatalf("missing refresh timings: %#v", cold.Refresh)
	}
	warm := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	requireRefreshStats(t, warm, history.RefreshStats{Checked: 1, Unchanged: 1})
	if len(warm.Reparsed) != 0 {
		t.Fatalf("warm files = %#v", warm.Reparsed)
	}
	appended := `{"type":"message","message":{"role":"user","content":"appended token"}}` + "\n"
	appendHistory(t, path, appended)
	resumed := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"appended token"}}, 1)
	requireRefreshStats(t, resumed, history.RefreshStats{Checked: 1, Resumed: 1, BytesParsed: int64(len(appended))})
	if len(resumed.Reparsed) != 0 {
		t.Fatalf("append files = %#v", resumed.Reparsed)
	}
	if diff := cmp.Diff(resumed.Refresh[0].RefreshStats, updates[len(updates)-1].Refresh); diff != "" {
		t.Fatalf("final progress differs (-result +progress):\n%s", diff)
	}
	if updates[0].Refresh.Reasons["new file"] != 1 || updates[0].Refresh.Resumed != 0 {
		t.Fatalf("previous progress changed: %#v", updates[0])
	}
}

func TestRefreshStatsExplainFullReparses(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"rewritten prefix", "incomplete checkpoint", "schema rebuild"} {
		t.Run(scenario, func(t *testing.T) {
			catalog, path := indexedFixture(t)
			if scenario == "incomplete checkpoint" {
				if err := os.WriteFile(path, []byte(strings.TrimSuffix(treeHistory, "\n")), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
			query := history.Query{Terms: []string{"refresh"}}
			mutateRefreshHistory(t, catalog, path, scenario)
			result := requireIndexedMatches(t, catalog, query, 1)
			if result.Refresh[0].Reparsed != 1 || result.Refresh[0].Resumed != 0 || result.Refresh[0].Reasons[scenario] != 1 || len(result.Reparsed) != 1 || result.Reparsed[0].Reason != scenario {
				t.Fatalf("%s: stats=%#v files=%#v", scenario, result.Refresh, result.Reparsed)
			}
		})
	}
}

func requireRefreshStats(t *testing.T, result history.Result, want history.RefreshStats) {
	t.Helper()
	if len(result.Refresh) != 1 {
		t.Fatalf("source refreshes = %#v", result.Refresh)
	}
	if diff := cmp.Diff(want, result.Refresh[0].RefreshStats, cmpopts.IgnoreFields(history.RefreshStats{}, "ParseTime", "StoreTime")); diff != "" {
		t.Fatalf("refresh stats (-want +got):\n%s", diff)
	}
}

func mutateRefreshHistory(t *testing.T, catalog history.Catalog, path, scenario string) {
	t.Helper()
	switch scenario {
	case "rewritten prefix":
		if err := os.WriteFile(path, []byte(strings.Replace(treeHistory, "handler", "service", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		appendHistory(t, path, "\n")
	case "incomplete checkpoint":
		appendHistory(t, path, "\n\n")
	case "schema rebuild":
		db := openTestIndex(t, catalog.IndexPath)
		if _, err := db.ExecContext(t.Context(), "PRAGMA user_version=4"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefreshStatsMarkUnavailableIndex(t *testing.T) {
	t.Parallel()
	catalog, _ := indexedFixture(t)
	catalog.IndexPath = t.TempDir()
	var last history.Progress
	catalog.Progress = func(progress history.Progress) { last = progress }
	for _, test := range []struct {
		query history.Query
		want  int
	}{
		{history.Query{Terms: []string{"refresh"}}, 1},
		{history.Query{Terms: []string{"refresh"}, IncludeTools: true}, 1},
		{history.Query{Terms: []string{"refresh"}, IncludeTools: true, Dirs: []string{"/elsewhere"}}, 0},
	} {
		result := requireIndexedMatches(t, catalog, test.query, test.want)
		if !result.IndexUnavailable || !last.IndexUnavailable || len(result.Refresh) != 0 {
			t.Fatalf("fallback stats = %#v, progress=%#v", result, last)
		}
	}
}

func TestRefreshStatsWhileWaitingForLockClaimNoWork(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	release := holdIndexWriteLock(t, catalog.IndexPath)
	defer release()
	appendHistory(t, path, "\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	result, err := catalog.Refresh(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || result.IndexUnavailable {
		t.Fatalf("waiting refresh = %#v, %v", result.Refresh, err)
	}
	requireRefreshStats(t, result, history.RefreshStats{Checked: 1})
}

func TestRefreshStatsExcludeCanceledWork(t *testing.T) {
	t.Parallel()
	catalog, _ := indexedFixture(t)
	writeHistory(t, catalog.Sources[0].Path, "z-second.jsonl", strings.ReplaceAll(treeHistory, "native-session", "second"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	catalog.Progress = func(progress history.Progress) {
		if progress.Refreshed == 1 {
			cancel()
		}
	}
	result, err := catalog.Refresh(ctx)
	if !errors.Is(err, context.Canceled) || len(result.Refresh) != 1 || result.Refresh[0].Reparsed != 1 || result.Refresh[0].Checked != 1 || result.Refresh[0].BytesParsed != int64(len(treeHistory)) {
		t.Fatalf("canceled refresh = %#v, %v", result.Refresh, err)
	}
}

func TestIndexRejectsMiddlePrefixRewriteBeforeAppend(t *testing.T) {
	t.Parallel()
	padding := `{"type":"message","message":{"role":"toolResult","content":"` + strings.Repeat("x", 128<<10) + `"}}` + "\n"
	body := `{"type":"session","id":"session","cwd":"/work"}` + "\n" +
		padding + `{"type":"message","message":{"role":"user","content":"middle-token"}}` + "\n" + padding
	path := writeHistory(t, t.TempDir(), "session.jsonl", body)
	catalog := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("pi"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"middle-token"}}, 1)

	rewritten := strings.Replace(body, "middle-token", "edited-token", 1) +
		`{"type":"message","message":{"role":"user","content":"appended-token"}}` + "\n"
	if err := os.WriteFile(path, []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"middle-token"}}, 0)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"edited-token"}}, 1)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"appended-token"}}, 1)
}

func TestIndexUpgradesWithoutReindexing(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	appendHistory(t, path, strings.Repeat(`{"type":"message","message":{"role":"user","content":"refresh token"}}`+"\n", 100))
	before := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	db := openTestIndex(t, catalog.IndexPath)
	_, err := db.ExecContext(t.Context(), `
DROP INDEX IF EXISTS parts_search;
DROP INDEX parts_conversation;
CREATE INDEX parts_conversation ON parts(conversation_id);
CREATE TRIGGER reject_reindex BEFORE UPDATE ON conversations BEGIN
 SELECT RAISE(FAIL,'transcripts must not be reindexed'); END;
PRAGMA user_version=2;`)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		after := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
		if diff := cmp.Diff(before, after, cmpopts.IgnoreFields(history.Result{}, "Refresh", "Reparsed")); diff != "" {
			t.Fatalf("cache upgrade changed results (-before +after):\n%s", diff)
		}
	}
}

func TestIndexRefreshesReplacementTruncationAndDeletion(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	replacement := writeHistory(t, t.TempDir(), "replacement", strings.ReplaceAll(treeHistory, "native-session", "replacement"))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	result := requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	if result.Matches[0].Conversation.SessionID != "replacement" {
		t.Fatal("atomic replacement retained old identity")
	}
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"empty"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 0)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 0)
	db := openTestIndex(t, c.IndexPath)
	var files int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&files); err != nil || files != 0 {
		t.Fatalf("deleted history retained: count=%d err=%v", files, err)
	}
}

func TestIndexRechecksIncompleteLastLineAndRetainsDiagnostics(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"unfinished`)
	for range 2 {
		searchWithRecordIssue(t, c, "refresh")
	}
	appendHistory(t, path, ` token"}}`+"\n")
	requireIndexedMatches(t, c, history.Query{Terms: []string{"unfinished token"}}, 1)
	appendHistory(t, path, "invalid secret-sentinel\n")
	result, err := c.Search(t.Context(), history.Query{Terms: []string{"refresh"}})
	if err != nil || len(result.Issues) != 1 {
		t.Fatalf("invalid record = %#v %v", result.Issues, err)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"after error"}}`+"\n")
	result = searchWithRecordIssue(t, c, "after error")
	if strings.Contains(result.Issues[0].Message, "secret-sentinel") {
		t.Fatal("cached diagnostic exposed content")
	}
}

func searchWithRecordIssue(t *testing.T, c history.Catalog, term string) history.Result {
	t.Helper()
	result, err := c.Search(t.Context(), history.Query{Terms: []string{term}})
	if err != nil || len(result.Issues) != 1 || !result.Issues[0].Record || len(result.Matches) != 1 {
		t.Fatalf("partial result = %#v, %v", result, err)
	}
	return result
}

func TestIndexToolProjectionExcludesReasoningAndSystemContent(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	if _, err := c.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	db := openTestIndex(t, c.IndexPath)
	var before, after [2]int64
	if err := db.QueryRowContext(t.Context(), "SELECT count(*),coalesce(sum(length(body)),0) FROM parts").Scan(&before[0], &before[1]); err != nil {
		t.Fatal(err)
	}
	for _, tools := range []bool{false, true, false, true} {
		want := 0
		if tools {
			want = 1
		}
		result := requireIndexedMatches(t, c, history.Query{Terms: []string{"tool-only"}, IncludeTools: tools}, want)
		requireRefreshStats(t, result, history.RefreshStats{Checked: 1, Unchanged: 1})
		for _, text := range []string{"reasoning-only", "system-only"} {
			requireIndexedMatches(t, c, history.Query{Terms: []string{text}, IncludeTools: tools}, 0)
		}
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*),coalesce(sum(length(body)),0) FROM parts").Scan(&after[0], &after[1]); err != nil || before != after {
		t.Fatalf("tools searches changed indexed text: before=%v after=%v err=%v", before, after, err)
	}
	var forbidden int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE instr(body,'reasoning-only')>0 OR instr(body,'system-only')>0 OR instr(body,'tool-only')>0").Scan(&forbidden); err != nil || forbidden != 0 {
		t.Fatalf("excluded text indexed: %d %v", forbidden, err)
	}
}

func TestToolsSearchSkipsExcludedUnreadableFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "selected.jsonl", strings.ReplaceAll(treeHistory, "/work/project/sub", "/work/selected"))
	excluded := writeHistory(t, root, "excluded.jsonl", strings.ReplaceAll(strings.ReplaceAll(treeHistory, "/work/project/sub", "/work/excluded"), "native-session", "excluded"))
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("pi"), Path: root}}) {
		t.Run(mode.name, func(t *testing.T) {
			requireToolsExcludedFileUnread(t, mode.catalog, excluded)
		})
	}
}

func requireToolsExcludedFileUnread(t *testing.T, catalog history.Catalog, excluded string) {
	t.Helper()
	blocked := false
	t.Cleanup(func() {
		if err := os.Chmod(excluded, 0o600); err != nil {
			t.Error(err)
		}
	})
	catalog.Progress = func(progress history.Progress) {
		if !blocked && progress.Total == 2 && progress.Done == 2 {
			blocked = true
			if err := os.Chmod(excluded, 0); err != nil {
				t.Error(err)
			}
		}
	}
	result, err := catalog.Search(t.Context(), history.Query{Terms: []string{"tool-only"}, Role: "tool", Dirs: []string{"/work/selected"}})
	if err != nil || len(result.Matches) != 1 || len(result.Issues) != 0 || !blocked {
		t.Fatalf("matches=%#v issues=%#v blocked=%t err=%v", result.Matches, result.Issues, blocked, err)
	}
	if _, err := os.ReadFile(excluded); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("excluded file is readable: %v", err)
	}
}

func TestToolsSearchUsesBothModificationAndActivityTimes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	older := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		id, day string
		mtime   time.Time
	}{
		{"selected", "2026-09-03", recent},
		{"old-file", "2026-09-03", older},
		{"old-activity", "2026-09-01", recent},
	} {
		body := strings.ReplaceAll(strings.ReplaceAll(treeHistory, "native-session", fixture.id), "2026-09-01", fixture.day)
		path := writeHistory(t, root, fixture.id+".jsonl", body)
		if err := os.Chtimes(path, fixture.mtime, fixture.mtime); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("pi"), Path: root}}) {
		t.Run(mode.name, func(t *testing.T) {
			if _, err := mode.catalog.Refresh(t.Context()); err != nil && !errors.Is(err, history.ErrIndexUnavailable) {
				t.Fatal(err)
			}
			result, err := mode.catalog.Search(t.Context(), history.Query{Terms: []string{"tool-only"}, IncludeTools: true, Since: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)})
			if err != nil || len(result.Matches) != 1 || result.Matches[0].Conversation.SessionID != "selected" {
				t.Fatalf("matches=%#v err=%v", result.Matches, err)
			}
		})
	}
}

func TestToolsStreamCancellationRetainsDeliveredMatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, id := range []string{"first", "second", "third"} {
		writeHistory(t, root, id+".jsonl", strings.ReplaceAll(treeHistory, "native-session", id))
	}
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("pi"), Path: root}}) {
		t.Run(mode.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var delivered []history.Match
			result, err := mode.catalog.Stream(ctx, history.Query{Terms: []string{"tool-only"}, IncludeTools: true}, func(match history.Match) bool {
				delivered = append(delivered, match)
				cancel()
				return false
			})
			if !errors.Is(err, context.Canceled) || len(delivered) != 1 || len(result.Matches) != 0 || delivered[0].Excerpts[0].Role != "tool" {
				t.Fatalf("delivered=%#v result=%#v err=%v", delivered, result, err)
			}
		})
	}
}

func TestIndexRebuildDiscardsPersistedToolText(t *testing.T) {
	t.Parallel()
	catalog, _ := indexedFixture(t)
	if _, err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	database := openTestIndex(t, catalog.IndexPath)
	if _, err := database.ExecContext(t.Context(), `ALTER TABLE files ADD COLUMN tools INTEGER NOT NULL DEFAULT 1;
INSERT INTO parts(conversation_id,role,body,folded,message_id,line,timestamp)
SELECT id,'tool','stale-tool','stale-tool','old-tool',1,'' FROM conversations;
PRAGMA user_version=5;`); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"stale-tool"}}, 0)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"tool-only"}, IncludeTools: true}, 1)
}

func TestIndexSearchesWhileWriteLocked(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	release := holdIndexWriteLock(t, c.IndexPath)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	// A warm search reads the last committed snapshot without a write lock: it
	// must not wait for another writer to release its lock.
	start := time.Now()
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("warm search waited %s on the index write lock", waited)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"locked append"}}`+"\n")
	c.Progress = func(progress history.Progress) {
		if progress.Waiting && !released {
			release()
			released = true
		}
	}
	result, err := c.Search(t.Context(), history.Query{Terms: []string{"locked append"}})
	if err != nil || len(result.Matches) != 1 || !released {
		t.Fatalf("locked refresh = %#v, released=%t, %v", result, released, err)
	}
	var stored int
	if err := openTestIndex(t, c.IndexPath).QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE instr(body,'locked append')>0").Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("waited search did not persist its result: count=%d err=%v", stored, err)
	}
}

func TestIndexWaitCanBeCancelled(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	release := holdIndexWriteLock(t, catalog.IndexPath)
	defer release()
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"new append"}}`+"\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var canceled time.Time
	catalog.Progress = func(progress history.Progress) {
		if progress.Waiting && canceled.IsZero() {
			canceled = time.Now()
			cancel()
		}
	}
	_, err := catalog.Search(ctx, history.Query{Terms: []string{"new append"}})
	if !errors.Is(err, context.Canceled) || canceled.IsZero() {
		t.Fatalf("canceled wait: canceled=%v err=%v", canceled, err)
	}
	if elapsed := time.Since(canceled); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestIndexHealsUnusableDefaultCache(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", temp)
	t.Setenv("HOME", temp)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeHistory(t, root, "session.jsonl", treeHistory)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("pi"), Path: root}}}
	path := filepath.Join(cache, "aht", "history-v1.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := openTestIndex(t, path)
	if _, err := foreign.ExecContext(t.Context(), "CREATE TABLE unrelated(value); INSERT INTO unrelated VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)

	// The unusable cache is deleted rather than kept beside the rebuilt index.
	if _, err := os.Stat(path + ".invalid"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unusable cache copy = %v", err)
	}
	var unrelated int
	if err := openTestIndex(t, path).QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name='unrelated'").Scan(&unrelated); err != nil || unrelated != 0 {
		t.Fatalf("rebuilt cache kept foreign tables: %d, %v", unrelated, err)
	}
}

func TestIndexRefreshesSidecarMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHistory(t, root, "session.messages.json", `{"sessionId":"s","messages":[{"role":"user","content":"needle"}]}`)
	manifest := writeHistory(t, root, "session.json", `{"session_id":"s","cwd":"/old","metadata":{"title":"old"}}`)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("cline"), Path: root}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"needle"}, Dirs: []string{"/old"}}, 1)
	if err := os.WriteFile(manifest, []byte(`{"session_id":"s","cwd":"/new","metadata":{"title":"new"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"needle"}, Dirs: []string{"/old"}}, 0)
	result := requireIndexedMatches(t, c, history.Query{Terms: []string{"needle"}, Dirs: []string{"/new"}}, 1)
	if result.Matches[0].Conversation.Title != "new" {
		t.Fatal("stale title")
	}
}

func TestIndexRefreshesSQLiteWALUpdatesAndDeletes(t *testing.T) {
	t.Parallel()
	db := databaseFixture(t, "state.db", `CREATE TABLE sessions(id,title,started_at,ended_at); CREATE TABLE messages(id,session_id,role,content,timestamp);
INSERT INTO sessions VALUES('s','title',1,2); INSERT INTO messages VALUES(1,'s','user','before',1);`)
	c := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("hermes"), Path: databasePath(t, db)}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"before"}}, 1)
	if _, err := db.ExecContext(t.Context(), "UPDATE messages SET content='after'"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"before"}}, 0)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"after"}}, 1)
	if _, err := db.ExecContext(t.Context(), "DELETE FROM messages"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"after"}}, 0)
}

func TestIndexConcurrentSearchAndCancellation(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			result, err := c.Search(t.Context(), history.Query{Terms: []string{"refresh"}})
			if err != nil || len(result.Matches) != 1 {
				t.Errorf("concurrent search: %#v %v", result, err)
			}
		})
	}
	group.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Search(ctx, history.Query{Terms: []string{"refresh"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search: %v", err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
}

func openTestIndex(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestIndexRefreshFailureDoesNotReturnStaleMatches(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	db := openTestIndex(t, c.IndexPath)
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER reject_refresh BEFORE INSERT ON parts BEGIN SELECT RAISE(ABORT,'test refresh failure'); END`); err != nil {
		t.Fatal(err)
	}
	appendHistory(t, path, `{"type":"message","message":{"role":"user","content":"appended token"}}`+"\n")
	result, err := c.Search(t.Context(), history.Query{Terms: []string{"refresh"}})
	if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 || len(result.Issues) != 1 {
		t.Fatalf("failed refresh returned stale content: %#v, %v", result, err)
	}
	var parts int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM parts WHERE instr(body,'appended token')>0").Scan(&parts); err != nil || parts != 0 {
		t.Fatalf("failed refresh partially committed: %d, %v", parts, err)
	}
	if _, err := db.ExecContext(t.Context(), "DROP TRIGGER reject_refresh"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"appended token"}}, 1)
}

func TestIndexScansAroundForeignDatabaseAndCanBeRebuilt(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	db := openTestIndex(t, c.IndexPath)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE retained(value); INSERT INTO retained VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	var value string
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM retained").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("changed foreign database: %q, %v", value, err)
	}
	// Use a fresh path, then exercise rebuilding the disposable cache.
	c.IndexPath = filepath.Join(t.TempDir(), "index.sqlite")
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	info, err := os.Stat(c.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cache permissions: %v", info.Mode())
	}
	if err := os.Remove(c.IndexPath); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
}

func TestIndexRechecksKimiMetadataAndSourceSelection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	session := "sessions/wd_project_0123456789ab/native-session/"
	manifest := writeHistory(t, root, session+"state.json", `{"id":"native-session","title":"First title","cwd":"/work/example-project"}`)
	path := writeHistory(t, root, session+"agents/main/wire.jsonl", kimiMirroredWireHistory)
	catalog := history.Catalog{Sources: []history.Source{{Harness: registry.Harness("kimi-code"), Path: path}}, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	result := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Dirs: []string{"/work/example-project"}}, 1)
	if result.Matches[0].Conversation.Messages != 2 || result.Matches[0].MatchingParts != 2 {
		t.Fatalf("indexed mirrored history = %#v", result)
	}
	result = requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Dirs: []string{"/work/example-project"}}, 1)
	if result.Matches[0].Conversation.Messages != 2 || result.Matches[0].MatchingParts != 2 {
		t.Fatalf("cached mirrored history = %#v", result)
	}
	if result.Matches[0].Conversation.Title != "First title" {
		t.Fatalf("initial title = %q", result.Matches[0].Conversation.Title)
	}
	if err := os.WriteFile(manifest, []byte(`{"id":"native-session","title":"Updated title","cwd":"/work/other-project"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Dirs: []string{"/work/example-project"}}, 0)
	result = requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Dirs: []string{"/work/other-project"}}, 1)
	if result.Matches[0].Conversation.Title != "Updated title" {
		t.Fatalf("updated title = %q", result.Matches[0].Conversation.Title)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Dirs: []string{"/work/other-project"}}, 0)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	catalog.Sources = []history.Source{}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 0)
}

func TestIndexDeletesVanishedHistoriesForEveryToolMode(t *testing.T) {
	t.Parallel()
	c, path := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"tool-only"}, IncludeTools: true}, 1)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// One vanished history leaves no rows behind for either search mode.
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 0)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"tool-only"}, IncludeTools: true}, 0)
	db := openTestIndex(t, c.IndexPath)
	var files, parts int
	if err := db.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM files),(SELECT count(*) FROM parts)").Scan(&files, &parts); err != nil || files != 0 || parts != 0 {
		t.Fatalf("vanished history retained: files=%d parts=%d, %v", files, parts, err)
	}
}

// holdIndexWriteLock takes the index write lock on a separate connection and
// returns a function that releases it.
func holdIndexWriteLock(t *testing.T, path string) func() {
	t.Helper()
	conn, err := openTestIndex(t, path).Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
			t.Error(err)
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}
}

func TestIndexRebuildsOlderOwnSchema(t *testing.T) {
	t.Parallel()
	c, _ := indexedFixture(t)
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	old := openTestIndex(t, c.IndexPath)
	var current int
	if err := old.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&current); err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(t.Context(), "PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, c, history.Query{Terms: []string{"refresh"}}, 1)
	var version int
	if err := openTestIndex(t, c.IndexPath).QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != current {
		t.Fatalf("schema version = %d, %v; want %d", version, err, current)
	}
}

func TestWarmIndexReportsUnreadableTranscript(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of their permission bits")
	}
	catalog, path := indexedFixture(t)
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 1} {
		result, err := catalog.Search(t.Context(), history.Query{Terms: []string{"refresh"}, Limit: limit})
		if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 || len(result.Issues) != 1 || result.Sources[0].Status != "failed" {
			t.Fatalf("unreadable history: result=%#v err=%v", result, err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
}

func TestWarmIndexExplicitSymlinkSource(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	link := filepath.Join(t.TempDir(), "selected.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	catalog.Sources = []history.Source{{Harness: registry.Harness("pi"), Path: link}}
	for range 2 {
		result := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Limit: 1}, 1)
		if result.Matches[0].Conversation.Path != path || len(result.Matches[0].Excerpts) == 0 {
			t.Fatalf("symlink result = %#v", result)
		}
	}
}

func TestLimitedIndexMatchesUnlimitedDetails(t *testing.T) {
	t.Parallel()
	sources := orderingSources(t)
	catalog := history.Catalog{Sources: sources, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}
	query := history.Query{Terms: []string{"refresh token"}, Registry: []registry.Session{
		{ID: "live", Harness: registry.Harness("pi"), SessionID: "recently-updated", SessionPath: sources[1].Path},
	}}
	for range 2 {
		all := requireIndexedMatches(t, catalog, query, 2)
		query.Limit = 1
		limited := requireIndexedMatches(t, catalog, query, 1)
		if diff := cmp.Diff(all.Matches[:1], limited.Matches); diff != "" {
			t.Fatalf("limited details (-unlimited +limited):\n%s", diff)
		}
		if !limited.Truncated || len(limited.Matches[0].RegistryStates) != 1 {
			t.Fatalf("limited result = %#v", limited)
		}
		query.Limit = 0
		// Change the winner between searches, exercising deferred reads after a refresh.
		appendHistory(t, sources[0].Path, `{"type":"message","id":"later","timestamp":"2026-09-21T00:00:00Z","message":{"role":"user","content":"refresh token latest"}}`+"\n")
		query.Registry[0].SessionID = "recently-created"
		query.Registry[0].SessionPath = sources[0].Path
	}
}

func TestIndexSearchFollowsSourceSelectionAfterRemoval(t *testing.T) {
	t.Parallel()
	catalog, first := indexedFixture(t)
	second := writeHistory(t, t.TempDir(), "other.jsonl", treeHistory)
	catalog.Sources = append(catalog.Sources, history.Source{Harness: registry.Harness("pi"), Path: second})
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 2)
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	catalog.Sources = []history.Source{{Harness: registry.Harness("pi"), Path: first}}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
	catalog.Sources = []history.Source{{Harness: registry.Harness("pi"), Path: filepath.Dir(second)}}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 0)
}

func TestWarmIndexSymlinkedParentSource(t *testing.T) {
	t.Parallel()
	realRoot := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "symlink-parent")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	path := writeHistory(t, symlinkRoot, "session.jsonl", treeHistory)
	link := filepath.Join(symlinkRoot, "selected.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	catalog := history.Catalog{
		Sources:   []history.Source{{Harness: registry.Harness("pi"), Path: link}},
		IndexPath: filepath.Join(t.TempDir(), "history.sqlite"),
	}
	for range 2 {
		result := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}, Limit: 1}, 1)
		if result.Matches[0].Conversation.Path != path || len(result.Matches[0].Excerpts) == 0 {
			t.Fatalf("symlink result = %#v", result)
		}
	}
}

func TestLimitedIndexMatchesSymlinkedParent(t *testing.T) {
	t.Parallel()
	realRoot := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "symlink-parent")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	created := writeHistory(t, symlinkRoot, "a-recently-created.jsonl", `{"type":"session","id":"recently-created","cwd":"/work/project","timestamp":"2026-09-20T08:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-20T08:00:01Z","message":{"role":"user","content":"refresh token"}}
{"type":"message","id":"a1","timestamp":"2026-09-20T08:30:00Z","message":{"role":"assistant","content":"refresh token again"}}
`)
	updated := writeHistory(t, symlinkRoot, "b-recently-updated.jsonl", `{"type":"session","id":"recently-updated","cwd":"/work/project","timestamp":"2026-09-19T00:00:00Z"}
{"type":"message","id":"u1","timestamp":"2026-09-19T00:00:01Z","message":{"role":"user","content":"refresh token"}}
{"type":"message","id":"a1","timestamp":"2026-09-20T09:00:00Z","message":{"role":"assistant","content":"refresh token again"}}
`)
	sources := []history.Source{{Harness: registry.Harness("pi"), Path: created}, {Harness: registry.Harness("pi"), Path: updated}}
	catalog := history.Catalog{Sources: sources, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}
	query := history.Query{Terms: []string{"refresh token"}, Limit: 1, Registry: []registry.Session{
		{ID: "live", Harness: registry.Harness("pi"), SessionID: "recently-updated", SessionPath: sources[1].Path},
	}}
	limited := requireIndexedMatches(t, catalog, query, 1)
	if len(limited.Matches[0].RegistryStates) != 1 {
		t.Fatalf("live match failed under symlink parent: %#v", limited.Matches[0].RegistryStates)
	}
}

func TestIndexSearchSymlinkedParentAfterRemoval(t *testing.T) {
	t.Parallel()
	realRoot := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "symlink-parent")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	first := writeHistory(t, symlinkRoot, "session.jsonl", treeHistory)
	second := writeHistory(t, t.TempDir(), "other.jsonl", treeHistory)
	catalog := history.Catalog{
		Sources:   []history.Source{{Harness: registry.Harness("pi"), Path: symlinkRoot}, {Harness: registry.Harness("pi"), Path: second}},
		IndexPath: filepath.Join(t.TempDir(), "history.sqlite"),
	}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 2)
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	catalog.Sources = []history.Source{{Harness: registry.Harness("pi"), Path: first}}
	requireIndexedMatches(t, catalog, history.Query{Terms: []string{"refresh"}}, 1)
}

func TestTranscriptActivityFallbackTimes(t *testing.T) {
	t.Parallel()
	older := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	recent := older.Add(48 * time.Hour)
	root := t.TempDir()
	fixtures := []transcriptActivityFixture{
		{"old", "", older, time.Time{}, older},
		{"recent", "", recent, time.Time{}, recent},
		{"message", `{"type":"message","message":{"role":"user","content":"needle"}}` + "\n", recent.Add(time.Hour), time.Time{}, recent.Add(time.Hour)},
		{"native-created", "", recent.Add(2 * time.Hour), older, time.Time{}},
		{"native-message", `{"type":"message","timestamp":"2026-09-01T00:00:00Z","message":{"role":"user","content":"needle"}}` + "\n", recent.Add(3 * time.Hour), older, older},
	}
	for _, fixture := range fixtures {
		header := `{"type":"session","id":"` + fixture.id + `"`
		if fixture.id == "native-created" {
			header += `,"timestamp":"2026-09-01T00:00:00Z"`
		}
		path := writeHistory(t, root, fixture.id+".jsonl", header+"}\n"+fixture.records)
		if err := os.Chtimes(path, fixture.mtime, fixture.mtime); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("pi"), Path: root}}) {
		t.Run(mode.name, func(t *testing.T) {
			for range 2 {
				requireTranscriptActivityTimes(t, mode.catalog, fixtures)
				requireTranscriptActivityBounds(t, mode.catalog, older, recent)
				requireIndexedMatches(t, mode.catalog, history.Query{Terms: []string{"needle"}, Since: recent.Add(-time.Hour)}, 1)
			}
		})
	}
}

type transcriptActivityFixture struct {
	id      string
	records string
	mtime   time.Time
	created time.Time
	updated time.Time
}

func requireTranscriptActivityTimes(t *testing.T, catalog history.Catalog, fixtures []transcriptActivityFixture) {
	t.Helper()
	result, err := catalog.List(t.Context(), history.ListQuery{})
	if err != nil || len(result.Matches) != len(fixtures) {
		t.Fatalf("list = %#v, %v", result, err)
	}
	if result.Matches[0].Conversation.SessionID != "message" || result.Matches[1].Conversation.SessionID != "recent" {
		t.Fatalf("fallback activity sorting = %#v", result.Matches)
	}
	for _, match := range result.Matches {
		for _, fixture := range fixtures {
			if c := match.Conversation; c.SessionID == fixture.id && (!c.CreatedAt.Equal(fixture.created) || !c.UpdatedAt.Equal(fixture.updated)) {
				t.Fatalf("%s times = %s / %s; want %s / %s", fixture.id, c.CreatedAt, c.UpdatedAt, fixture.created, fixture.updated)
			}
		}
	}
}

func requireTranscriptActivityBounds(t *testing.T, catalog history.Catalog, older, recent time.Time) {
	t.Helper()

	for _, test := range []struct {
		filter history.Filter
		want   []string
	}{
		{history.Filter{Since: older.Add(time.Hour)}, []string{"message", "recent"}},
		{history.Filter{Until: older.Add(time.Hour)}, []string{"native-created", "native-message", "old"}},
		{history.Filter{Since: recent.Add(-time.Hour), Until: recent.Add(time.Minute)}, []string{"recent"}},
	} {
		filtered, filterErr := catalog.List(t.Context(), history.ListQuery{Filter: test.filter})
		ids := make([]string, 0, len(filtered.Matches))
		for _, match := range filtered.Matches {
			ids = append(ids, match.Conversation.SessionID)
		}
		slices.Sort(ids)
		if diff := cmp.Diff(test.want, ids); filterErr != nil || diff != "" {
			t.Fatalf("filter %#v: %v (-want +got):\n%s", test.filter, filterErr, diff)
		}
	}
}

func TestTranscriptActivityFallbackAppend(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"indexed", "direct"} {
		t.Run(name, func(t *testing.T) {
			path := writeHistory(t, t.TempDir(), "session.jsonl", `{"type":"session","id":"session"}`+"\n"+`{"type":"message","message":{"role":"user","content":"needle"}}`+"\n")
			sources := []history.Source{{Harness: registry.Harness("pi"), Path: path}}
			catalog := history.Catalog{Sources: sources, IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
			if name == "direct" {
				catalog = directCatalog(t, sources)
			}
			fallback := time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)
			native := fallback.Add(-24 * time.Hour)
			for _, step := range []struct {
				record  string
				mtime   time.Time
				created time.Time
				updated time.Time
			}{
				{"", fallback, time.Time{}, fallback},
				{`{"type":"message","message":{"role":"assistant","content":"needle appended"}}`, fallback.Add(time.Hour), time.Time{}, fallback.Add(time.Hour)},
				{`{"type":"message","timestamp":"2026-09-09T00:00:00Z","message":{"role":"assistant","content":"native"}}`, fallback.Add(2 * time.Hour), native, native},
				{`{"type":"session_info","name":"metadata-only change"}`, fallback.Add(3 * time.Hour), native, native},
				{`{"type":"message","timestamp":"2026-09-09T01:00:00Z","message":{"role":"assistant","content":"later native"}}`, fallback.Add(4 * time.Hour), native, native.Add(time.Hour)},
			} {
				if step.record != "" {
					appendHistory(t, path, step.record+"\n")
				}
				if err := os.Chtimes(path, step.mtime, step.mtime); err != nil {
					t.Fatal(err)
				}
				requireTranscriptConversationTimes(t, catalog, step.created, step.updated)
			}
		})
	}
}

func requireTranscriptConversationTimes(t *testing.T, catalog history.Catalog, created, updated time.Time) {
	t.Helper()
	for range 2 {
		result := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"needle"}}, 1)
		c := result.Matches[0].Conversation
		if !c.CreatedAt.Equal(created) || !c.UpdatedAt.Equal(updated) {
			t.Fatalf("times = %s / %s; want %s / %s", c.CreatedAt, c.UpdatedAt, created, updated)
		}
	}
}

func TestTranscriptActivityFallbackForDocuments(t *testing.T) {
	t.Parallel()
	path := writeHistory(t, t.TempDir(), "session.messages.json", `{"sessionId":"session","messages":[{"role":"user","content":"needle"}]}`)
	modified := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("cline"), Path: path}}) {
		t.Run(mode.name, func(t *testing.T) {
			requireTranscriptConversationTimes(t, mode.catalog, time.Time{}, modified)
			requireIndexedMatches(t, mode.catalog, history.Query{Terms: []string{"needle"}, Since: modified.Add(-time.Hour)}, 1)
		})
	}
}

func TestTranscriptActivityFallbackLeavesDatabaseTimesUnchanged(t *testing.T) {
	t.Parallel()
	db := databaseFixture(t, "state.db", `CREATE TABLE sessions(id,title,started_at,ended_at); CREATE TABLE messages(id,session_id,role,content,timestamp);
INSERT INTO sessions VALUES('unknown','unknown','',''),('native','native',1,2);
INSERT INTO messages VALUES(1,'unknown','user','needle',''),(2,'native','user','needle',1);`)
	path := databasePath(t, db)
	mtime := time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	for _, mode := range searchModes(t, []history.Source{{Harness: registry.Harness("hermes"), Path: path}}) {
		t.Run(mode.name, func(t *testing.T) {
			for range 2 {
				requireDatabaseActivityTimes(t, mode.catalog)
				requireIndexedMatches(t, mode.catalog, history.Query{Terms: []string{"needle"}, Since: mtime.Add(-time.Hour)}, 0)
			}
		})
	}
}

func requireDatabaseActivityTimes(t *testing.T, catalog history.Catalog) {
	t.Helper()
	result := requireIndexedMatches(t, catalog, history.Query{Terms: []string{"needle"}}, 2)
	for _, match := range result.Matches {
		c := match.Conversation
		if c.SessionID == "unknown" {
			if !c.CreatedAt.IsZero() || !c.UpdatedAt.IsZero() {
				t.Fatalf("database inherited file time: %#v", c)
			}
		} else if !c.CreatedAt.Equal(time.Unix(1, 0)) || !c.UpdatedAt.Equal(time.Unix(2, 0)) {
			t.Fatalf("native database times changed: %#v", c)
		}
	}
}
