package aht_test

import (
	"context"
	"encoding/json"
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

	_, err := aht.SearchHistory(ctx, aht.HistoryQuery{Terms: []string{"needle"}})
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
		result, err := catalog.Search(t.Context(), aht.HistoryQuery{Terms: []string{"needle"}})
		if err != nil {
			t.Fatalf("Search error = %v, want nil", err)
		}
		want := []string{"session-newer", "session-older", "session-undated"}
		if got := sessionIDs(result); !slices.Equal(got, want) {
			t.Fatalf("Search session order = %v, want %v", got, want)
		}
	})

	t.Run("limit keeps the newest conversations", func(t *testing.T) {
		result, err := catalog.Search(t.Context(), aht.HistoryQuery{Terms: []string{"needle"}, Limit: 1})
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

func TestListHistoryReadsDefaultSourcesWithoutText(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	sessions := filepath.Join(home, ".pi", "agent", "sessions", "project")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, cwd := range map[string]string{"session-app": "/work/app", "session-other": "/work/other"} {
		body := fmt.Sprintf(`{"type":"session","id":%q,"cwd":%q,"timestamp":"2026-09-20T10:00:00Z"}`+"\n"+`{"type":"message","message":{"role":"user","content":"hello"},"timestamp":"2026-09-20T10:01:00Z"}`+"\n", id, cwd)
		if err := os.WriteFile(filepath.Join(sessions, id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := aht.ListHistory(t.Context(), aht.HistoryListQuery{Harnesses: []aht.Harness{aht.HarnessPi}, Dir: "/work/app"})
	if err != nil {
		t.Fatalf("ListHistory error = %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Conversation.SessionID != "session-app" || len(result.Matches[0].ResumeCommand) == 0 {
		t.Fatalf("ListHistory = %#v", result.Matches)
	}
}

func TestHistoryQueryKeepsTextAndHarnessFields(t *testing.T) {
	path := writePiHistory(t, "session-text", "needle beside the haystack", time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC))
	catalog := aht.HistoryCatalog{
		Sources:   []aht.HistorySource{{Harness: aht.HarnessPi, Path: path}, {Harness: aht.HarnessCodex, Path: filepath.Dir(path)}},
		IndexPath: filepath.Join(t.TempDir(), "history.sqlite"),
	}
	for _, tt := range []struct {
		name  string
		query aht.HistoryQuery
		want  int
	}{
		{"text alone", aht.HistoryQuery{Text: "needle", Harness: aht.HarnessPi, Dir: "/work/app", Registry: nil, IgnoreHarnesses: nil, IgnorePaths: nil}, 1},
		{"text and terms must all match", aht.HistoryQuery{Text: "needle", Terms: []string{"absent"}}, 0},
		{"harness narrows sources", aht.HistoryQuery{Text: "needle", Harness: aht.HarnessCodex}, 0},
	} {
		result, err := catalog.Search(t.Context(), tt.query)
		if err != nil || len(result.Matches) != tt.want {
			t.Fatalf("%s: matches = %d, err = %v", tt.name, len(result.Matches), err)
		}
	}
	if _, err := catalog.Search(t.Context(), aht.HistoryQuery{Text: "needle", Harness: "nope"}); !errors.Is(err, aht.ErrInvalidHistoryQuery) {
		t.Fatalf("unknown harness error = %v", err)
	}

	var decoded aht.HistoryQuery
	if err := json.Unmarshal([]byte(`{"text":"needle","harness":"pi","dir":"/work/app","limit":1}`), &decoded); err != nil {
		t.Fatal(err)
	}
	result, err := catalog.Search(t.Context(), decoded)
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("decoded query %#v: matches = %d, err = %v", decoded, len(result.Matches), err)
	}
}
