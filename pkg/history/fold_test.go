package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zigai/aht/pkg/registry"
)

// foldFixtureBody is a native transcript whose user message spells one token in
// every case-folding shape the normalizer must equate.
const foldFixtureBody = `{"type":"session","id":"fold-session","cwd":"/work/fold"}
{"type":"message","id":"u1","message":{"role":"user","content":"Straße ς ﬃ İSTANBUL"}}
`

// foldSearch runs the production direct scan over one Pi-format transcript.
func foldSearch(t *testing.T, body, query string) (Result, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{Sources: []Source{{Harness: registry.Harness("pi"), Path: path}}}
	return catalog.searchDirect(t.Context(), Query{Text: query})
}

// TestFoldNormalizerContract holds every expectation that depends on the folding
// implementation, from the fold seam itself to matching through a real search.
// Swapping the normalizer means revisiting exactly this test.
func TestFoldNormalizerContract(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, text, want string }{
		{"ascii", "Refresh TOKEN", "refresh token"},
		{"sharp s", "Straße", "strasse"},
		{"capital sharp s", "ẞ", "ss"},
		{"final sigma", "ΟΔΟΣ", "οδοσ"},
		{"medial sigma", "Σίσυφος", "σίσυφοσ"},
		{"ffi ligature", "ﬃ", "ffi"},
		{"turkish dotted i", "İSTANBUL", "istanbul"},
	} {
		t.Run("fold "+tt.name, func(t *testing.T) {
			t.Parallel()
			if got := fold(tt.text); got != tt.want {
				t.Fatalf("fold(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}

	// An expanding fold advances folded offsets faster than unfolded rune
	// indices, so the mapping must return the rune whose fold covers the offset.
	for _, tt := range []struct {
		name         string
		text         string
		foldedOffset int
		want         int
	}{
		{"start of an expansion", "ßneedle", 0, 0},
		{"one rune into an expansion", "ßneedle", 1, 1},
		{"after an expansion", "ßneedle", 2, 1},
		{"after repeated expansions", "ßß needle", 5, 3},
		{"beyond the folded text", "abc", 99, 3},
		{"negative offset", "abc", -3, 0},
		{"empty text", "", 4, 0},
	} {
		t.Run("foldRuneIndex "+tt.name, func(t *testing.T) {
			t.Parallel()
			if got := foldRuneIndex(tt.text, tt.foldedOffset); got != tt.want {
				t.Fatalf("foldRuneIndex(%q, %d) = %d, want %d", tt.text, tt.foldedOffset, got, tt.want)
			}
		})
	}

	// The equivalences must hold for real searches too: each query is written in
	// a different spelling than the transcript it has to match.
	for _, tt := range []struct{ name, query, spelling string }{
		{"sharp s", "STRASSE", "Straße"},
		{"final sigma", "Σ", "ς"},
		{"ffi ligature", "ffi", "ﬃ"},
		{"turkish dotted i", "istanbul", "İSTANBUL"},
	} {
		t.Run("search "+tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := foldSearch(t, foldFixtureBody, tt.query)
			if err != nil || len(result.Matches) != 1 {
				t.Fatalf("search %q = %#v, %v", tt.query, result, err)
			}
			if len(result.Matches[0].Excerpts) != 1 || !strings.Contains(result.Matches[0].Excerpts[0].Text, tt.spelling) {
				t.Fatalf("search %q excerpts = %#v", tt.query, result.Matches[0].Excerpts)
			}
		})
	}
}

// TestFoldRuneIndexProperties proves the folded-to-unfolded mapping is exact at
// every rune boundary of texts whose folds expand, shrink, and stay equal.
func TestFoldRuneIndexProperties(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"",
		"plain ascii text",
		"Straße ẞ ﬃ ﬄ",
		"ΟΔΟΣ ς Σ σ Σίσυφος",
		"İSTANBUL ıIİ",
		strings.Repeat("ß", 64) + "needle" + strings.Repeat("ﬃ", 32),
		"e\u0301 combining \u00df",
		"\xff\xfe invalid \u00df",
	} {
		runes := []rune(text)
		for index := range len(runes) + 1 {
			foldedOffset := utf8.RuneCountInString(fold(string(runes[:index])))
			if got := foldRuneIndex(text, foldedOffset); got != index {
				t.Fatalf("foldRuneIndex(%q, %d) = %d, want %d", text, foldedOffset, got, index)
			}
		}
	}
}

// TestFoldExcerptWindows proves an excerpt window anchored through foldRuneIndex
// still contains the match when expansion-dense text precedes it, and that no
// body can push the window out of range.
func TestFoldExcerptWindows(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body, query, token string
	}{
		{
			name:  "sharp s run before the match",
			body:  strings.Repeat("ß", 300) + " refresh token " + strings.Repeat("ß", 300),
			query: "refresh token",
			token: "refresh token",
		},
		{
			name:  "ligature matched inside its own expansion",
			body:  strings.Repeat("ß", 300) + "ﬃ" + strings.Repeat("ß", 300),
			query: "ffi",
			token: "ﬃ",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			message, err := json.Marshal(map[string]any{"type": "message", "id": "u1", "message": map[string]any{"role": "user", "content": tt.body}})
			if err != nil {
				t.Fatal(err)
			}
			fixture := `{"type":"session","id":"fold-session","cwd":"/work/fold"}` + "\n" + string(message) + "\n"
			result, err := foldSearch(t, fixture, tt.query)
			if err != nil || len(result.Matches) != 1 || len(result.Matches[0].Excerpts) != 1 {
				t.Fatalf("search %q = %#v, %v", tt.query, result, err)
			}
			excerpt := result.Matches[0].Excerpts[0].Text
			if !strings.Contains(excerpt, tt.token) {
				t.Fatalf("excerpt for %q does not contain %q: %q", tt.query, tt.token, excerpt)
			}
			if runes := utf8.RuneCountInString(excerpt); runes > excerptRunes+2 {
				t.Fatalf("excerpt of %d runes exceeds the %d-rune window: %q", runes, excerptRunes, excerpt)
			}
		})
	}
}
