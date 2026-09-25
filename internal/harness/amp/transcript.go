package amp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func readTranscriptDocument(ctx context.Context, t *transcript.Decoder, data []byte) error {
	var r transcript.Record
	if json.Unmarshal(data, &r) != nil {
		return transcript.ErrInvalidRecord
	}
	sessionID := transcript.Str(r, "id")
	if sessionID == "" {
		return transcript.ErrUnknownFormat
	}
	messages, err := parseAmpMessages(r["messages"])
	if err != nil {
		return err
	}
	*t.Recognized = true
	t.Conversation.SessionID = sessionID
	t.Conversation.CreatedAt = transcript.ParseTime(r["created"])
	t.Conversation.Title = transcript.Str(r, "title")
	if dir := ampDirectory(transcript.Obj(r, "env")); dir != "" {
		t.Conversation.CWD = dir
		t.Conversation.ProjectRoot = dir
	}
	for line, msg := range messages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		t.Message(ctx, msg, ampMessageID(msg), line+1, ampMessageTimestamp(msg))
	}
	return nil
}

func parseAmpMessages(raw json.RawMessage) ([]transcript.Record, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var messages []transcript.Record
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, transcript.ErrUnknownFormat
	}
	return messages, nil
}

func ampDirectory(env transcript.Record) string {
	if len(env) == 0 {
		return ""
	}
	initialObj := transcript.Obj(env, "initial")
	if len(initialObj) == 0 {
		return ""
	}
	var trees []transcript.Record
	if json.Unmarshal(initialObj["trees"], &trees) != nil || len(trees) == 0 {
		return ""
	}
	uri := transcript.Str(trees[0], "uri")
	if dir, ok := strings.CutPrefix(uri, "file://"); ok {
		return dir
	}
	return ""
}

func ampMessageTimestamp(msg transcript.Record) time.Time {
	if ts := transcript.ParseTime(transcript.Obj(msg, "meta")["sentAt"]); !ts.IsZero() {
		return ts
	}
	return transcript.ParseTime(msg["timestamp"])
}

func ampMessageID(msg transcript.Record) string {
	if id := transcript.Str(msg, "id"); id != "" {
		return id
	}
	if rawID, ok := msg["messageId"]; ok && len(rawID) > 0 {
		return strings.Trim(string(rawID), `"`)
	}
	return ""
}

func (ampHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"T-*.json", "*.json"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, Document: readTranscriptDocument, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("AMP_DATA_DIR", filepath.Join(transcript.DataHome(home), "amp")), "threads")}
}
