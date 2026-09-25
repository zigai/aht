package agy

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (agyHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: nil, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(home, ".gemini", "antigravity-cli")}
}
