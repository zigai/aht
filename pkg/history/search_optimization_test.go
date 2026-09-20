package history_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/pkg/history"
	"github.com/zigai/aht/pkg/registry"
)

func TestWarmIndexReportsUnreadableTranscript(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of their permission bits")
	}
	catalog, path := indexedFixture(t)
	requireIndexedMatches(t, catalog, history.Query{Text: "refresh"}, 1)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 1} {
		result, err := catalog.Search(t.Context(), history.Query{Text: "refresh", Limit: limit})
		if !errors.Is(err, history.ErrIncomplete) || len(result.Matches) != 0 || len(result.Issues) != 1 || result.Sources[0].Status != "failed" {
			t.Fatalf("unreadable history: result=%#v err=%v", result, err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	requireIndexedMatches(t, catalog, history.Query{Text: "refresh"}, 1)
}

func TestWarmIndexExplicitSymlinkSource(t *testing.T) {
	t.Parallel()
	catalog, path := indexedFixture(t)
	link := filepath.Join(t.TempDir(), "selected.jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	catalog.Sources = []history.Source{{Harness: registry.HarnessPi, Path: link}}
	for range 2 {
		result := requireIndexedMatches(t, catalog, history.Query{Text: "refresh", Limit: 1}, 1)
		if result.Matches[0].Conversation.Path != path || len(result.Matches[0].Excerpts) == 0 {
			t.Fatalf("symlink result = %#v", result)
		}
	}
}

func TestLimitedIndexMatchesUnlimitedDetails(t *testing.T) {
	t.Parallel()
	sources := orderingSources(t)
	catalog := history.Catalog{Sources: sources, IndexPath: filepath.Join(t.TempDir(), "history.sqlite")}
	query := history.Query{Text: "refresh token", Registry: []registry.Session{
		{ID: "live", Harness: registry.HarnessPi, SessionID: "recently-updated", SessionPath: sources[1].Path},
	}}
	for range 2 {
		all := requireIndexedMatches(t, catalog, query, 2)
		query.Limit = 1
		limited := requireIndexedMatches(t, catalog, query, 1)
		if diff := cmp.Diff(all.Matches[:1], limited.Matches); diff != "" {
			t.Fatalf("limited details (-unlimited +limited):\n%s", diff)
		}
		if !limited.Truncated || len(limited.Matches[0].Live) != 1 {
			t.Fatalf("limited result = %#v", limited)
		}
		query.Limit = 0
		// Change the winner between searches, exercising deferred reads after a refresh.
		appendHistory(t, sources[0].Path, `{"type":"message","id":"later","timestamp":"2026-09-21T00:00:00Z","message":{"role":"user","content":"refresh token latest"}}`+"\n")
		query.Registry[0].SessionID = "recently-created"
		query.Registry[0].SessionPath = sources[0].Path
	}
}

func TestIndexCleanupOnlyVisitsSelectedSources(t *testing.T) {
	t.Parallel()
	catalog, first := indexedFixture(t)
	second := writeHistory(t, t.TempDir(), "other.jsonl", treeHistory)
	catalog.Sources = append(catalog.Sources, history.Source{Harness: registry.HarnessPi, Path: second})
	requireIndexedMatches(t, catalog, history.Query{Text: "refresh"}, 2)
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	catalog.Sources = []history.Source{{Harness: registry.HarnessPi, Path: first}}
	requireIndexedMatches(t, catalog, history.Query{Text: "refresh"}, 1)
	db := openTestIndex(t, catalog.IndexPath)
	var files int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&files); err != nil || files != 2 {
		t.Fatalf("unselected source was cleaned up: files=%d err=%v", files, err)
	}
	catalog.Sources = []history.Source{{Harness: registry.HarnessPi, Path: filepath.Dir(second)}}
	requireIndexedMatches(t, catalog, history.Query{Text: "refresh"}, 0)
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM files").Scan(&files); err != nil || files != 1 {
		t.Fatalf("selected vanished source retained: files=%d err=%v", files, err)
	}
}
