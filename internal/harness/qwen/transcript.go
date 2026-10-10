package qwen

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	c := t.Conversation
	kind := transcript.Str(r, "type")
	switch kind {
	case "user", "assistant", "tool_result", "system":
	default:
		return
	}
	sessionID := transcript.Str(r, "sessionId")
	if sessionID == "" {
		return
	}
	*t.Recognized = true
	c.SessionID = sessionID
	if cwd := transcript.Str(r, "cwd"); cwd != "" {
		c.CWD = cwd
	}
	if branch := transcript.Str(r, "gitBranch"); branch != "" {
		c.GitBranch = branch
	}
	if kind == "system" {
		if transcript.Str(r, "subtype") == "custom_title" {
			readTitleRecord(c, transcript.Obj(r, "systemPayload"))
		}
		return
	}
	if model := transcript.Str(r, "model"); kind == "assistant" && model != "" {
		c.Model = model
	}
	readMessageParts(ctx, t, kind, transcript.Obj(r, "message"), transcript.Str(r, "uuid"), line, transcript.ParseTime(r["timestamp"]))
}

func readTitleRecord(c *transcript.Conversation, payload transcript.Record) {
	title := transcript.Str(payload, "customTitle")
	if title == "" {
		return
	}
	if transcript.Str(payload, "titleSource") == "auto" {
		c.AITitle = title
		c.CustomTitle = ""
	} else {
		c.CustomTitle = title
		c.AITitle = ""
	}
	c.Title = title
}

func readMessageParts(ctx context.Context, t *transcript.Decoder, kind string, message transcript.Record, id string, line int, at time.Time) {
	var parts []transcript.Record
	if json.Unmarshal(message["parts"], &parts) != nil {
		return
	}
	role := kind
	if kind == "tool_result" {
		role = "tool"
	}
	var text strings.Builder
	for _, part := range parts {
		if call := transcript.Obj(part, "functionCall"); call != nil {
			t.Capture(ctx, "tool", transcript.Str(call, "name")+" "+string(call["args"]), id, line, at)
			continue
		}
		if response := transcript.Obj(part, "functionResponse"); response != nil {
			t.Capture(ctx, "tool", functionResponseText(transcript.Obj(response, "response")), id, line, at)
			continue
		}
		if string(part["thought"]) == "true" {
			continue
		}
		if body := transcript.Str(part, "text"); body != "" {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(body)
		}
	}
	if text.Len() > 0 {
		t.Capture(ctx, role, text.String(), id, line, at)
	}
}

func functionResponseText(response transcript.Record) string {
	for _, key := range []string{"output", "error"} {
		if body := transcript.Str(response, key); body != "" {
			return body
		}
	}
	data, err := json.Marshal(response)
	if err != nil || response == nil {
		return ""
	}
	return string(data)
}

// Telemetry and other system records make up most of a session file. Once
// a record has identified the session, only custom_title records among them
// carry indexed metadata.
func readFastRecord(_ context.Context, t *transcript.Decoder, data []byte, _ int) bool {
	if !*t.Recognized || !transcript.HasRecordHint(data, []byte(`"type":"system"`)) || transcript.HasRecordHint(data, []byte(`"subtype":"custom_title"`)) {
		return false
	}
	var entry struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(data, &entry) != nil {
		return false
	}
	return entry.Type == "system" && entry.Subtype != "custom_title"
}

func (qwenHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.jsonl"}, Sources: transcriptSources, SkipDirectory: skipTranscriptDirectory, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil, LocalTitles: false, Parent: subagentParent}
}

func transcriptSources(home string) []string {
	base := runtimeDirectory(home)
	if base == "" {
		return nil
	}
	return []string{filepath.Join(base, "projects")}
}

// Sessions live in <project>/chats and subagent transcripts in
// <project>/subagents/<session>; sources may start above or inside a project.
func skipTranscriptDirectory(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if parts[0] == "projects" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "chats", "subagents":
		return false
	case "memory", "workflows":
		return true
	}
	return len(parts) >= 2 && parts[1] != "chats" && parts[1] != "subagents"
}

// subagentParent maps <project>/subagents/<session>/agent-<id>.jsonl to the
// session's <project>/chats/<session>.jsonl.
func subagentParent(path string) string {
	sessionDir := filepath.Dir(path)
	subagents := filepath.Dir(sessionDir)
	if filepath.Base(subagents) != "subagents" {
		return ""
	}
	return filepath.Join(filepath.Dir(subagents), "chats", filepath.Base(sessionDir)+".jsonl")
}
