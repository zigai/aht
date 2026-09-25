package kilo

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (kiloHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"kilo*.db", "opencode*.db"}, Sources: transcriptSources, SkipDirectory: skipTranscriptDirectory, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: transcriptQuery}
}

func transcriptSources(home string) []string {
	return []string{transcript.DatabaseLocation(filepath.Join(transcript.DataHome(home), "kilo"), "KILO_DB")}
}
func skipTranscriptDirectory(path string) bool { return path != "." }
