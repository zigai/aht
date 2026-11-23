package droid

import (
	"context"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func (droidHarness) Transcript() transcript.Reader {
	return transcript.Reader{
		Patterns: []string{"*.jsonl"},
		Sources:  transcriptSources,
		Record:   readTranscriptRecord,
	}
}

func transcriptSources(home string) ([]string, error) {
	if dir := droidSessionsDir(home); dir != "" {
		return []string{dir}, nil
	}

	return nil, nil
}

// Droid stores the session identity and title in session_start, and records
// each turn's messages separately. Validated against a native v0.238.0 log.
func readTranscriptRecord(
	ctx context.Context,
	decoder *transcript.Decoder,
	record transcript.Record,
	line int,
) {
	conversation := decoder.Conversation

	switch transcript.Str(record, "type") {
	case "session_start":
		if id := transcript.Str(record, "id"); id != "" {
			*decoder.Recognized = true
			conversation.SessionID = id
			conversation.Title = transcript.Str(record, "title")
			conversation.CWD = transcript.Str(record, "cwd")
		}
	case "message":
		if !*decoder.Recognized {
			return
		}

		native := transcript.Obj(record, "message")

		message := transcript.Message{
			Role:      transcript.Str(native, "role"),
			Model:     transcript.Str(native, "modelId"),
			Content:   native["content"],
			ToolCalls: nil,
		}
		if message.Role == "user" && transcript.Str(native, "visibility") == "llm_only" {
			message.Role = transcript.InjectedUserRole
		}

		message.CaptureModel(decoder)
		message.Capture(
			ctx,
			decoder,
			transcript.Str(record, "id"),
			line,
			transcript.ParseTime(record["timestamp"]),
		)
	}
}
