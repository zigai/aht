package cline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/internal/harness/transcript"
)

func readTranscriptDocument(ctx context.Context, t *transcript.Decoder, data []byte) error {
	var r transcript.Record
	if json.Unmarshal(data, &r) != nil {
		return transcript.ErrInvalidRecord
	}
	var messages []transcript.Record
	if transcript.Str(r, "sessionId") == "" || json.Unmarshal(r["messages"], &messages) != nil {
		return transcript.ErrUnknownFormat
	}
	*t.Recognized = true
	t.Conversation.SessionID = transcript.Str(r, "sessionId")
	t.Conversation.UpdatedAt = transcript.ParseTime(r["updated_at"])
	clineMetadata(t)
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read transcript: %w", err)
		}
		t.Message(ctx, message, transcript.Str(message, "id"), 0, transcript.ParseTime(message["timestamp"]))
	}
	return nil
}

func clineMetadata(t *transcript.Decoder) {
	path := t.Conversation.Path
	// Only the sibling manifest is consulted; embedded messages_path values are
	// never followed and cannot redirect discovery outside the selected source.
	name := strings.TrimSuffix(filepath.Base(path), ".messages.json") + ".json"
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Issue(path, err)
		return
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Issue(path, err)
		}
	}()
	file, err := root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Issue(path, err)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Issue(path, err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, transcript.MaxRecordBytes+1))
	if err != nil {
		t.Issue(path, err)
		return
	}
	if len(data) > transcript.MaxRecordBytes {
		t.Issue(path, transcript.ErrRecordSize)
		return
	}
	var r transcript.Record
	if err := json.Unmarshal(data, &r); err != nil {
		t.Issue(path, transcript.ErrInvalidRecord)
		return
	}
	if transcript.Str(r, "session_id") != t.Conversation.SessionID {
		return
	}
	t.Conversation.CWD = transcript.Str(r, "cwd")
	t.Conversation.ProjectRoot = transcript.Str(r, "workspace_root")
	t.Conversation.CreatedAt = transcript.ParseTime(r["started_at"])
	t.Conversation.Title = transcript.Str(transcript.Obj(r, "metadata"), "title")
}

func transcriptExtra(path string, _ map[string]string, stamp func(string) string) string {
	return stamp(strings.TrimSuffix(path, ".messages.json") + ".json")
}

func (clineHarness) Transcript() transcript.Reader {
	return transcript.Reader{Patterns: []string{"*.messages.json"}, Sources: transcriptSources, SkipDirectory: nil, SourceMetadata: nil, Initialize: nil, Extra: transcriptExtra, Record: nil, Document: readTranscriptDocument, Query: nil}
}

func transcriptSources(home string) []string {
	return []string{transcript.EnvPath("CLINE_SESSION_DATA_DIR", filepath.Join(transcript.EnvPath("CLINE_DATA_DIR", filepath.Join(home, ".cline", "data")), "sessions"))}
}
