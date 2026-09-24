package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type TreeEntry struct {
	Type, ID, CWD, Title, Name string
	Timestamp                  time.Time
}

func TreeRecord(ctx context.Context, t *Decoder, r Record, line int) {
	entry := TreeMetadata(r)
	switch entry.Type {
	case "session":
		*t.Recognized = true
		t.Conversation.SessionID = entry.ID
		t.Conversation.CWD = entry.CWD
		if value := entry.Timestamp; !value.IsZero() {
			t.Conversation.CreatedAt = value
		}
		if title := entry.Title; title != "" {
			t.Conversation.Title = title
		}
	case "session_info":
		t.Conversation.Title = entry.Name
	case "title", "title_change":
		if title := entry.Title; title != "" {
			t.Conversation.Title = title
		}
	case "message":
		t.Message(ctx, Obj(r, "message"), entry.ID, line, entry.Timestamp)
	}
}

func TreeMetadata(r Record) TreeEntry {
	return TreeEntry{Type: Str(r, "type"), ID: Str(r, "id"), CWD: Str(r, "cwd"), Title: Str(r, "title"), Name: Str(r, "name"), Timestamp: ParseTime(r["timestamp"])}
}

func ParseTree(data []byte) (TreeEntry, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return TreeEntry{Type: "", ID: "", CWD: "", Title: "", Name: "", Timestamp: time.Time{}}, fmt.Errorf("decode transcript metadata: %w", err)
	}
	return TreeMetadata(r), nil
}
