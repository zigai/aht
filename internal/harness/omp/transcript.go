package omp

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (ompHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: transcript.TreeRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(home, ".omp", "agent", "sessions")}
}
