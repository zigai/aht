package hermes

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (hermesHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"state.db"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: nil, Query: transcriptQuery}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("HERMES_HOME", filepath.Join(home, ".hermes")), "state.db")}
}
