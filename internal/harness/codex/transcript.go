package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

const typedUserProjection = "typed-users"

type turnEvent struct {
	Type string `json:"type"`
	Item struct {
		Type string `json:"type"`
	} `json:"item"`
}

func (event turnEvent) typedUser() bool {
	return event.Type == "item_completed" && event.Item.Type == "UserMessage"
}

func readTranscriptRecord(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	payload := transcript.Obj(r, "payload")
	switch transcript.Str(r, "type") {
	case "session_meta":
		*t.Recognized = true
		t.Conversation.SessionID = transcript.Str(payload, "id")
		t.Conversation.CWD = transcript.Str(payload, "cwd")
		t.Conversation.CreatedAt = transcript.ParseTime(payload["timestamp"])
		if branch := transcript.Str(transcript.Obj(payload, "git"), "branch"); branch != "" {
			t.Conversation.GitBranch = branch
		}
	case "event_msg":
		if item := typedUserItem(payload); item != nil && t.Projection == typedUserProjection {
			for _, part := range transcript.MessageParts(item["content"], transcript.TypedUserRole, false) {
				t.Capture(ctx, part.Role, part.Text, transcript.Str(item, "id"), line, transcript.ParseTime(r["timestamp"]))
			}
		}
	case "response_item":
		timestamp := transcript.ParseTime(r["timestamp"])
		switch transcript.Str(payload, "type") {
		case "message":
			readResponseMessage(ctx, t, r, line)
		case "function_call", "custom_tool_call":
			t.Capture(ctx, "tool", transcript.Str(payload, "name")+" "+transcript.FirstString(payload, "arguments", "input"), transcript.Str(payload, "call_id"), line, timestamp)
		case "function_call_output", "custom_tool_call_output":
			t.Capture(ctx, "tool", transcript.ContentText(payload["output"]), transcript.Str(payload, "call_id"), line, timestamp)
		}
	}
}

func readResponseMessage(ctx context.Context, t *transcript.Decoder, r transcript.Record, line int) {
	payload := transcript.Obj(r, "payload")
	if transcript.Str(payload, "channel") == "analysis" {
		return
	}
	if transcript.Str(payload, "role") == "user" && t.Projection == typedUserProjection {
		payload["role"] = json.RawMessage(`"` + transcript.InjectedUserRole + `"`)
	}
	t.Message(ctx, payload, transcript.Str(payload, "id"), line, transcript.ParseTime(r["timestamp"]))
}

func readFastRecord(ctx context.Context, t *transcript.Decoder, data []byte, line int) bool {
	var entry struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if jsonv2.Unmarshal(data, &entry) != nil {
		return false
	}
	switch entry.Type {
	case "event_msg":
		var payload turnEvent
		return json.Unmarshal(entry.Payload, &payload) == nil && !payload.typedUser()
	case "compacted", "token_usage_record", "inter_agent_communication_metadata", "world_state", "realtime_item":
		return true
	case "turn_context":
		var turn struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(entry.Payload, &turn) == nil && turn.Model != "" {
			t.Conversation.Model = turn.Model
		}
		return true
	case "session_meta":
		return readSessionMetadata(t, entry.Payload)
	case "response_item":
		if fastResponseItem(ctx, t, entry.Payload, entry.Timestamp, line) {
			return true
		}
		return readResponseItem(ctx, t, entry.Payload, entry.Timestamp, line)
	}
	return false
}

func fastResponseItem(ctx context.Context, t *transcript.Decoder, raw, timestamp json.RawMessage, line int) bool {
	var payload struct {
		Type    string `json:"type"`
		Channel string `json:"channel"`
		CallID  string `json:"call_id"`
		Role    string `json:"role"`
	}
	if jsonv2.Unmarshal(raw, &payload) != nil {
		return false
	}
	switch payload.Type {
	case "message":
		return payload.Channel == "analysis"
	case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
		if !t.IncludeTools {
			t.Capture(ctx, "tool", "", payload.CallID, line, transcript.ParseTime(timestamp))
			return true
		}
	case "reasoning", "agent_message", "ghost_snapshot", "image_generation_call", "tool_search_call", "tool_search_output", "web_search_call":
		return payload.Role == ""
	}
	return false
}

func typedUserItem(payload transcript.Record) transcript.Record {
	if transcript.Str(payload, "type") != "item_completed" {
		return nil
	}
	item := transcript.Obj(payload, "item")
	if transcript.Str(item, "type") != "UserMessage" {
		return nil
	}
	return item
}

func (codexHarness) TranscriptProjection(ctx context.Context, file *os.File, size int64) (string, error) {
	reader := bufio.NewReader(io.NewSectionReader(file, 0, size))
	var buffer []byte
	for {
		data, readErr := readProjectionRecord(ctx, reader, &buffer)
		if errors.Is(readErr, transcript.ErrRecordSize) {
			continue
		}
		if bytes.Contains(data, []byte("item_completed")) {
			var record struct {
				Type    string    `json:"type"`
				Payload turnEvent `json:"payload"`
			}
			if json.Unmarshal(data, &record) == nil && record.Type == "event_msg" && record.Payload.typedUser() {
				return typedUserProjection, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return "", nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

func readProjectionRecord(ctx context.Context, reader *bufio.Reader, buffer *[]byte) ([]byte, error) {
	*buffer = (*buffer)[:0]
	oversized := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read transcript projection: %w", err)
		}
		chunk, readErr := reader.ReadSlice('\n')
		fragmented := errors.Is(readErr, bufio.ErrBufferFull)
		var err error
		if readErr != nil && !fragmented {
			err = fmt.Errorf("read transcript projection: %w", readErr)
		}
		if len(*buffer) == 0 && !oversized && !fragmented {
			return chunk, err
		}
		if !oversized && len(*buffer)+len(chunk) <= transcript.MaxRecordBytes {
			*buffer = append(*buffer, chunk...)
		} else {
			oversized = true
		}
		if fragmented {
			continue
		}
		if oversized {
			return nil, transcript.ErrRecordSize
		}
		return *buffer, err
	}
}

func readSessionMetadata(decoder *transcript.Decoder, raw json.RawMessage) bool {
	var payload struct {
		ID        string          `json:"id"`
		CWD       string          `json:"cwd"`
		Timestamp json.RawMessage `json:"timestamp"`
		Git       json.RawMessage `json:"git"`
	}
	if jsonv2.Unmarshal(raw, &payload) != nil {
		return false
	}
	var git struct {
		Branch string `json:"branch"`
	}
	if len(payload.Git) > 0 && jsonv2.Unmarshal(payload.Git, &git) != nil {
		return false
	}
	*decoder.Recognized = true
	decoder.Conversation.SessionID = payload.ID
	decoder.Conversation.CWD = payload.CWD
	decoder.Conversation.CreatedAt = transcript.ParseTime(payload.Timestamp)
	if git.Branch != "" {
		decoder.Conversation.GitBranch = git.Branch
	}
	return true
}

func readResponseItem(ctx context.Context, decoder *transcript.Decoder, raw, timestamp json.RawMessage, line int) bool {
	var payload struct {
		Type      string          `json:"type"`
		Channel   string          `json:"channel"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Input     string          `json:"input"`
		Output    json.RawMessage `json:"output"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	}
	if jsonv2.Unmarshal(raw, &payload) != nil {
		return false
	}
	at := transcript.ParseTime(timestamp)
	switch payload.Type {
	case "message":
		if payload.Channel != "analysis" {
			role := payload.Role
			if role == "user" && decoder.Projection == typedUserProjection {
				role = transcript.InjectedUserRole
			}
			message := transcript.Message{Role: role, Model: "", Content: payload.Content, ToolCalls: payload.ToolCalls}
			message.Capture(ctx, decoder, payload.ID, line, at)
		}
	case "function_call", "custom_tool_call":
		arguments := payload.Arguments
		if arguments == "" {
			arguments = payload.Input
		}
		decoder.Capture(ctx, "tool", payload.Name+" "+arguments, payload.CallID, line, at)
	case "function_call_output", "custom_tool_call_output":
		decoder.Capture(ctx, "tool", transcript.ContentText(payload.Output), payload.CallID, line, at)
	default:
		return false
	}
	return true
}

func (codexHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"rollout-*.jsonl"}, Sources: transcriptSources, SkipDirectory: nil, SkipFile: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: readTranscriptRecord, FastRecord: readFastRecord, Document: nil, Query: nil, LocalTitles: true, RewritableHeaders: nil, Parent: nil}
}

func transcriptSources(home string) ([]string, error) {
	return []string{filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "sessions"), filepath.Join(transcript.EnvPath("CODEX_HOME", filepath.Join(home, ".codex")), "archived_sessions")}, nil
}
