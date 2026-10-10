package qwen

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func TestTranscriptDirectories(t *testing.T) {
	t.Parallel()

	for path, skip := range map[string]bool{
		"-repo":                        false,
		"-repo/chats":                  false,
		"-repo/subagents":              false,
		"-repo/subagents/s1":           false,
		"-repo/memory":                 true,
		"-repo/workflows":              true,
		"-repo/subagents/s1/nested":    false,
		"projects":                     false,
		"projects/-repo":               false,
		"projects/-repo/chats":         false,
		"projects/-repo/chats/archive": false,
		"projects/-repo/subagents/s1":  false,
		"projects/-repo/memory":        true,
		"projects/-repo/workflows":     true,
		"chats":                        false,
		"chats/archive":                false,
		"subagents":                    false,
		"subagents/s1":                 false,
		"subagents/s1/nested":          false,
		"memory":                       true,
		"workflows":                    true,
		"-repo/other":                  true,
	} {
		if got := skipTranscriptDirectory(filepath.FromSlash(path)); got != skip {
			t.Fatalf("skipTranscriptDirectory(%q) = %t, want %t", path, got, skip)
		}
	}
}

func TestTranscriptLatestTitleSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		records    []string
		wantTitle  string
		wantCustom string
		wantAI     string
	}{
		{name: "auto replaces manual", records: []string{titleRecord("s1", "Manual", "manual"), titleRecord("s1", "Automatic", "auto")}, wantTitle: "Automatic", wantCustom: "", wantAI: "Automatic"},
		{name: "manual replaces auto", records: []string{titleRecord("s1", "Automatic", "auto"), titleRecord("s1", "Manual", "manual")}, wantTitle: "Manual", wantCustom: "Manual", wantAI: ""},
		{name: "empty title ignored", records: []string{titleRecord("s1", "Manual", "manual"), titleRecord("s1", "", "auto")}, wantTitle: "Manual", wantCustom: "Manual", wantAI: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var conversation transcript.Conversation
			recognized := false
			decoder := transcript.Decoder{Conversation: &conversation, Recognized: &recognized, Emit: nil, Issue: nil, Metadata: nil, IncludeTools: false}
			for line, data := range test.records {
				var record transcript.Record
				if err := json.Unmarshal([]byte(data), &record); err != nil {
					t.Fatal(err)
				}
				New().Transcript().Record(t.Context(), &decoder, record, line+1)
			}
			if !recognized || conversation.Title != test.wantTitle || conversation.CustomTitle != test.wantCustom || conversation.AITitle != test.wantAI {
				t.Fatalf("title metadata = %#v, want title %q, custom %q, AI %q", conversation, test.wantTitle, test.wantCustom, test.wantAI)
			}
		})
	}
}

func TestSubagentParent(t *testing.T) {
	t.Parallel()

	root := filepath.FromSlash("/home/user/.qwen/projects/-repo")
	if got, want := subagentParent(filepath.Join(root, "subagents", "s1", "agent-a1.jsonl")), filepath.Join(root, "chats", "s1.jsonl"); got != want {
		t.Fatalf("subagent parent = %q, want %q", got, want)
	}
	if got := subagentParent(filepath.Join(root, "chats", "s1.jsonl")); got != "" {
		t.Fatalf("session transcript has parent %q", got)
	}
}
