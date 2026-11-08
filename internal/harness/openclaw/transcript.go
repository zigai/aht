package openclaw

import (
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (openclawHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Patterns: []string{
			"*.jsonl",
			"openclaw-agent.sqlite",
			"*.jsonl.deleted.*.zst",
			"*.jsonl.reset.*.zst",
			"*.jsonl.deleted.*",
			"*.jsonl.reset.*",
		},
		Sources:       transcriptSources,
		FileFormat:    transcriptFormat,
		SkipDirectory: skipTranscriptDirectory,
		Record:        transcript.TreeRecord,
		Query:         transcriptQuery,
	}
}

func transcriptSources(home string) ([]string, error) {
	state := openclawStateDir(home)
	if state == "" {
		return nil, nil
	}

	return []string{filepath.Join(state, "agents")}, nil
}

func transcriptFormat(path string) transcript.Format {
	switch filepath.Ext(path) {
	case ".sqlite", ".db":
		return transcript.FormatDatabase
	case ".zst":
		return transcript.FormatCompressed
	default:
		return transcript.FormatJSONL
	}
}

func skipTranscriptDirectory(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	return len(parts) > 1 && parts[1] != "agent" && parts[1] != "sessions"
}
