package cline

import (
	"encoding/json"
	"testing"
)

func TestClineHistoryEntryDecodesNativeSessionID(t *testing.T) {
	var entry clineHistoryEntry
	if err := json.Unmarshal([]byte(`{"sessionId":"session-1","metadata":{"title":"Cline title"}}`), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.SessionID != "session-1" {
		t.Fatalf("session ID = %q, want %q", entry.SessionID, "session-1")
	}
	if entry.Metadata.Title != "Cline title" {
		t.Fatalf("title = %q, want %q", entry.Metadata.Title, "Cline title")
	}
}
