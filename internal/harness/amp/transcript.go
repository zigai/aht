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
	t.Conversation.GitBranch = ampBranch(transcript.Obj(r, "env"))
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

func ampTree(env transcript.Record) transcript.Record {
	var trees []transcript.Record
	if json.Unmarshal(transcript.Obj(env, "initial")["trees"], &trees) != nil || len(trees) == 0 {
		return nil
	}
	return trees[0]
}

func ampBranch(env transcript.Record) string {
	ref := transcript.Str(transcript.Obj(ampTree(env), "repository"), "ref")
	if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return branch
	}
	return ""
}

func ampDirectory(env transcript.Record) string {
	uri := transcript.Str(ampTree(env), "uri")
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
	return transcript.Reader{Patterns: []string{"T-*.json", "*.json"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: nil, Record: nil, FastRecord: nil, Document: readTranscriptDocument, Query: nil, LocalTitles: false, Parent: nil}
}

func transcriptSources(home string) []string {
	return []string{filepath.Join(transcript.EnvPath("AMP_DATA_DIR", filepath.Join(transcript.DataHome(home), "amp")), "threads")}
}
