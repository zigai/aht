package droid_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/droid"
	"github.com/zigai/aht/v2/pkg/history"
)

func TestSearchDroidTranscriptFromSessionsDirectory(t *testing.T) {
	const (
		sessionID = "4471b6fa-01fd-4d71-83b6-89760bebd623"
		prompt    = "aht native transcript boundary fixture"
	)

	// Captured from Droid 0.238.0 in a fakeprovider scratch home; the
	// working path and injected context are redacted, the records are native.
	data, err := os.ReadFile("testdata/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	id := droid.New().Definition().ID

	for _, name := range []string{"default", "override"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			sessions := filepath.Join(home, ".factory", "sessions")
			override := ""

			if name == "override" {
				sessions = filepath.Join(t.TempDir(), "custom-sessions")
				override = " " + sessions + " "
			}

			t.Setenv("DROID_SESSIONS_DIR", override)

			workspace := filepath.Join(sessions, "-work-fixture")
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatal(err)
			}

			sessionPath := filepath.Join(workspace, sessionID+".jsonl")
			if err := os.WriteFile(sessionPath, data, 0o600); err != nil {
				t.Fatal(err)
			}

			defaults, err := history.DefaultSources()
			if err != nil {
				t.Fatal(err)
			}

			var sources []history.Source

			for _, source := range defaults {
				if source.Harness == id {
					sources = append(sources, source)
				}
			}

			if len(sources) != 1 || sources[0].Path != sessions {
				t.Fatalf("Droid sources = %+v, want %s", sources, sessions)
			}

			c := history.Catalog{
				Sources:   sources,
				IndexPath: filepath.Join(t.TempDir(), "index.sqlite"),
			}

			found, err := c.Search(t.Context(), history.Query{Text: "native transcript boundary"})
			if err != nil || len(found.Matches) != 1 {
				t.Fatalf(
					"Droid search matches = %+v, sources = %+v, error = %v",
					found.Matches,
					found.Sources,
					err,
				)
			}

			match := found.Matches[0]

			got := match.Conversation
			if got.Path != sessionPath || got.SessionID != sessionID ||
				got.CWD != "/work/fixture" ||
				got.Title != "Fake session" ||
				got.Prompt != prompt ||
				got.Messages != 2 {
				t.Fatalf("Droid conversation = %+v", got)
			}

			if len(match.Excerpts) != 2 || match.Excerpts[0].Role != history.RoleUser ||
				match.Excerpts[1].Role != history.RoleAssistant {
				t.Fatalf(
					"Droid excerpts = %+v, want user prompt then assistant reply",
					match.Excerpts,
				)
			}

			hidden, err := c.Search(t.Context(), history.Query{Text: "Fixture context only"})
			if err != nil || len(hidden.Matches) != 0 {
				t.Fatalf("default search exposed injected context: %+v, %v", hidden.Matches, err)
			}

			contextual, err := c.Search(
				t.Context(),
				history.Query{Text: "Fixture context only", Role: history.RoleContext},
			)
			if err != nil || len(contextual.Matches) != 1 ||
				len(contextual.Matches[0].Excerpts) != 1 ||
				contextual.Matches[0].Excerpts[0].Role != history.RoleContext {
				t.Fatalf(
					"context search = %+v, %v; want one context excerpt",
					contextual.Matches,
					err,
				)
			}
		})
	}
}
