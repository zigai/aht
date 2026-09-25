package aht_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/aht"
)

func BenchmarkLookupTitlesCodexBatch(b *testing.B) {
	home := b.TempDir()
	var index strings.Builder
	sessions := make([]aht.Session, 100)
	for i := range sessions {
		id := "thread-" + strconv.Itoa(i)
		index.WriteString(`{"id":"` + id + `","thread_name":"Named thread"}` + "\n")
		sessions[i] = aht.Session{Harness: aht.HarnessCodex, SessionID: id, SessionPath: filepath.Join(home, "sessions", "rollout.jsonl")}
	}
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := aht.LookupTitles(b.Context(), sessions); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookupTitlesCodexStateBatch(b *testing.B) {
	home := b.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(b.Context(), "CREATE TABLE threads (id TEXT PRIMARY KEY, history_mode TEXT, title TEXT, first_user_message TEXT, name TEXT)"); err != nil {
		b.Fatal(err)
	}
	sessions := make([]aht.Session, 100)
	for i := range sessions {
		id := "thread-" + strconv.Itoa(i)
		if _, err := db.ExecContext(b.Context(), "INSERT INTO threads VALUES (?, 'paginated', '', '', 'Named thread')", id); err != nil {
			b.Fatal(err)
		}
		sessions[i] = aht.Session{Harness: aht.HarnessCodex, SessionID: id, SessionPath: filepath.Join(home, "sessions", "rollout.jsonl")}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := aht.LookupTitles(b.Context(), sessions); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookupTitlesOMPSlot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"title","title":"Named session"}`+"\n"+`{"type":"session","id":"native"}`+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	sessions := []aht.Session{{Harness: aht.HarnessOmp, SessionID: "native", SessionPath: path}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := aht.LookupTitles(b.Context(), sessions); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookupTitlesPiTranscript(b *testing.B) {
	path := filepath.Join(b.TempDir(), "session.jsonl")
	var transcript strings.Builder
	transcript.WriteString(`{"type":"session","id":"native"}` + "\n")
	transcript.WriteString(`{"type":"session_info","name":"Named session"}` + "\n")
	for range 10000 {
		transcript.WriteString(`{"type":"message","message":{"role":"assistant","content":"ordinary response"}}` + "\n")
	}
	if err := os.WriteFile(path, []byte(transcript.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	sessions := []aht.Session{{Harness: aht.HarnessPi, SessionID: "native", SessionPath: path}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := aht.LookupTitles(b.Context(), sessions); err != nil {
			b.Fatal(err)
		}
	}
}
