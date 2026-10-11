package grok

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (grokHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"grok.db"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, FastRecord: nil, Document: nil, Query: transcriptQuery, LocalTitles: false, Parent: nil}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(home, ".grok", "grok.db")}, nil
}
