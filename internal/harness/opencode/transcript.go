package opencode

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (opencodeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"opencode*.db"}, Sources: transcriptSources, SkipDirectory: skipTranscriptDirectory, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: transcriptQuery}
}

func transcriptSources(home string) []string {
	return []string{transcript.DatabaseLocation(filepath.Join(transcript.DataHome(home), "opencode"), "OPENCODE_DB")}
}
func skipTranscriptDirectory(path string) bool { return path != "." }
