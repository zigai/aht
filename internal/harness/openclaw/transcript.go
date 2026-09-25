package openclaw

import (
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (openclawHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"openclaw-agent.sqlite", "*.jsonl", "*.jsonl.deleted.*", "*.jsonl.reset.*"}, Sources: transcriptSources, SkipDirectory: skipTranscriptDirectory, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: transcript.TreeRecord, Document: nil, Query: transcriptQuery}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("OPENCLAW_STATE_DIR", filepath.Join(home, ".openclaw")), "agents")}
}

func skipTranscriptDirectory(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	return len(parts) > 1 && parts[1] != "agent" && parts[1] != "sessions"
}
