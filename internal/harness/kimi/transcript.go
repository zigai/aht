package kimi

import (
	"context"
	"path/filepath"
	"time"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, decoder *transcript.Decoder, record transcript.Record, line int) {
	if kimiTranscriptSessionDir(decoder.Conversation.Path) == "" {
		return
	}
	if agentID := transcript.Str(record, "agentId"); agentID != "" && agentID != "main" {
		return
	}
	timestamp := transcript.ParseTime(record["time"])
	switch transcript.Str(record, "type") {
	case "metadata":
		if transcript.Str(record, "protocol_version") == "" {
			return
		}
		*decoder.Recognized = true
		if decoder.Conversation.CreatedAt.IsZero() {
			decoder.Conversation.CreatedAt = transcript.ParseTime(record["created_at"])
		}
	case "config.update":
		if decoder.Conversation.CWD == "" {
			decoder.Conversation.CWD = transcript.Str(record, "cwd")
		}
	case "context.append_message":
		readTranscriptMessage(ctx, decoder, transcript.Obj(record, "message"), line, timestamp)
	case "context.append_loop_event":
		readTranscriptLoopEvent(ctx, decoder, transcript.Obj(record, "event"), line, timestamp)
	case "llm.request":
		decoder.Conversation.Model = transcript.Str(record, "model")
	}
}

func readTranscriptMessage(ctx context.Context, decoder *transcript.Decoder, message transcript.Record, line int, timestamp time.Time) {
	if transcript.Str(message, "role") == "user" {
		origin := transcript.Obj(message, "origin")
		switch transcript.Str(origin, "kind") {
		case "injection", "system_trigger", "retry", "compaction_summary":
			return
		case "skill_activation", "plugin_command":
			if transcript.Str(origin, "trigger") != "user-slash" {
				return
			}
		}
	}
	decoder.Message(ctx, message, transcript.Str(message, "id"), line, timestamp)
	decoder.Capture(ctx, "tool", string(message["toolCalls"]), "", line, timestamp)
}

func readTranscriptLoopEvent(ctx context.Context, decoder *transcript.Decoder, event transcript.Record, line int, timestamp time.Time) {
	switch transcript.Str(event, "type") {
	case "content.part":
		part := transcript.Obj(event, "part")
		if transcript.Str(part, "type") == "text" {
			decoder.Capture(ctx, "assistant", transcript.Str(part, "text"), transcript.Str(event, "stepUuid"), line, timestamp)
		}
	case "tool.call":
		decoder.Capture(ctx, "tool", transcript.Str(event, "name")+" "+string(event["args"]), transcript.Str(event, "toolCallId"), line, timestamp)
	case "tool.result":
		result := transcript.Obj(event, "result")
		decoder.Capture(ctx, "tool", transcript.ContentText(result["output"]), transcript.Str(event, "toolCallId"), line, timestamp)
	}
}

func (kimiCodeHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"wire.jsonl"}, Sources: transcriptSources, SkipDirectory: skipTranscriptDirectory, SourceMetadata: nil, Initialize: initializeTranscript, Extra: transcriptExtra, Record: readTranscriptRecord, FastRecord: nil, Document: nil, Query: nil, LocalTitles: true, Parent: nil}
}

func skipTranscriptDirectory(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "agents" && filepath.Base(path) != "main"
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(transcript.EnvPath("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code")), "sessions")}, nil
}
