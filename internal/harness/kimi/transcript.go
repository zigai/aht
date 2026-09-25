package kimi

import (
	"context"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	if transcript.Str(r, "role") != "" {
		*t.Recognized = true
		t.Message(ctx, r, transcript.Str(r, "id"), line, transcript.ParseTime(r["timestamp"]))
	}
}

func initializeTranscript(t *transcript.Decoder) {
	path := t.Conversation.Path
	t.Conversation.SessionID = filepath.Base(filepath.Dir(path))
	t.Conversation.CWD = t.Metadata[filepath.Base(filepath.Dir(filepath.Dir(path)))]
}

func transcriptExtra(path string, metadata map[string]string, _ func(string) string) string {
	return metadata[filepath.Base(filepath.Dir(filepath.Dir(path)))]
}

func (kimiCodeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"context.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: transcriptSourceMetadata, Initialize: initializeTranscript, Extra: transcriptExtra, Record: readTranscriptRecord, Document: nil, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("KIMI_SHARE_DIR", filepath.Join(home, ".kimi")), "sessions")}
}
