package grok

import (
	"path/filepath"

	"github.com/zigai/aht/internal/harness/transcript"
)

func (grokHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"grok.db"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: transcriptQuery}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(home, ".grok", "grok.db")}
}
