package aht_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/aht"
)

func TestSearchHistoryCanceledContext(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := aht.SearchHistory(ctx, aht.HistoryQuery{Text: "needle"})
	if err == nil {
		t.Fatal("SearchHistory with a canceled context returned no error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SearchHistory error = %v, want context.Canceled", err)
	}
	const prefix = "search history:"
	if count := strings.Count(err.Error(), prefix); count != 1 {
		t.Fatalf("SearchHistory error %q contains %d %q prefixes, want 1", err, count, prefix)
	}
}

func TestSearchHistoryEmptyText(t *testing.T) {
	_, err := aht.SearchHistory(t.Context(), aht.HistoryQuery{})
	if !errors.Is(err, aht.ErrInvalidHistoryQuery) {
		t.Fatalf("SearchHistory error = %v, want ErrInvalidHistoryQuery", err)
	}
}

func TestHistoryCatalogOrdersMatches(t *testing.T) {
	older := writePiHistory(t, "session-older", "needle in the older conversation", time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC))
	newer := writePiHistory(t, "session-newer", "needle in the newer conversation", time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC))
	undated := writePiHistory(t, "session-undated", "needle in the undated conversation", time.Time{})
	catalog := aht.HistoryCatalog{
		Sources: []aht.HistorySource{
			{Harness: aht.HarnessPi, Path: older},
			{Harness: aht.HarnessPi, Path: newer},
			{Harness: aht.HarnessPi, Path: undated},
		},
		IndexPath: filepath.Join(t.TempDir(), "history.sqlite"),
	}

	t.Run("most recently updated first", func(t *testing.T) {
		result, err := catalog.Search(t.Context(), aht.HistoryQuery{Text: "needle"})
		if err != nil {
			t.Fatalf("Search error = %v, want nil", err)
		}
		want := []string{"session-newer", "session-older", "session-undated"}
		if got := sessionIDs(result); !slices.Equal(got, want) {
			t.Fatalf("Search session order = %v, want %v", got, want)
		}
	})

	t.Run("limit keeps the newest conversations", func(t *testing.T) {
		result, err := catalog.Search(t.Context(), aht.HistoryQuery{Text: "needle", Limit: 1})
		if err != nil {
			t.Fatalf("Search error = %v, want nil", err)
		}
		want := []string{"session-newer"}
		if got := sessionIDs(result); !slices.Equal(got, want) {
			t.Fatalf("Search session order = %v, want %v", got, want)
		}
		if !result.Truncated {
			t.Fatal("Search Truncated = false, want true")
		}
	})
}

func sessionIDs(result aht.HistoryResult) []string {
	ids := make([]string, 0, len(result.Matches))
	for _, match := range result.Matches {
		ids = append(ids, match.Conversation.SessionID)
	}
	return ids
}

// writePiHistory writes a minimal Pi-format transcript and returns its path.
// Records carry no timestamps when at is zero.
func writePiHistory(t *testing.T, sessionID, text string, at time.Time) string {
	t.Helper()
	session := fmt.Sprintf(`{"type":"session","id":%q,"cwd":"/work/app"}`, sessionID)
	message := fmt.Sprintf(`{"type":"message","message":{"role":"user","content":%q}}`, text)
	if !at.IsZero() {
		session = fmt.Sprintf(`{"type":"session","id":%q,"cwd":"/work/app","timestamp":%q}`, sessionID, at.Add(-time.Minute).Format(time.RFC3339))
		message = fmt.Sprintf(`{"type":"message","message":{"role":"user","content":%q},"timestamp":%q}`, text, at.Format(time.RFC3339))
	}
	path := filepath.Join(t.TempDir(), sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(session+"\n"+message+"\n"), 0o600); err != nil {
		t.Fatalf("write pi history: %v", err)
	}
	return path
}
