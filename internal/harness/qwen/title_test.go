package qwen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func titleRecord(sessionID, title, source string) string {
	return `{"uuid":"x","sessionId":"` + sessionID + `","timestamp":"2026-09-01T00:00:00Z","type":"system","subtype":"custom_title","systemPayload":{"customTitle":"` + title + `","titleSource":"` + source + `"}}` + "\n"
}

func userRecord(sessionID, text string) string {
	return `{"uuid":"u","sessionId":"` + sessionID + `","timestamp":"2026-09-01T00:00:00Z","type":"user","message":{"role":"user","parts":[{"text":"` + text + `"}]}}` + "\n"
}

func writeSession(t *testing.T, path string, records ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(records, "")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lookupTitle(t *testing.T, identity registry.ObservationIdentity) string {
	t.Helper()
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{identity})
	if err != nil {
		t.Fatal(err)
	}
	return titles[0]
}

func TestSessionTitles(t *testing.T) {
	t.Parallel()

	padding := userRecord("s1", strings.Repeat("x", titleWindowBytes))
	tests := []struct {
		name    string
		records []string
		want    string
	}{
		{name: "latest title wins", records: []string{titleRecord("s1", "First", "auto"), userRecord("s1", "hello"), titleRecord("s1", "Renamed", "manual")}, want: "Renamed"},
		{name: "title beyond the tail window", records: []string{titleRecord("s1", "Early", "manual"), padding}, want: "Early"},
		{name: "tail title preferred over head", records: []string{titleRecord("s1", "Early", "auto"), padding, titleRecord("s1", "Late", "manual")}, want: "Late"},
		{name: "other session title ignored", records: []string{titleRecord("other", "Foreign", "manual")}, want: ""},
		{name: "quoted marker in user text ignored", records: []string{userRecord("s1", `\"type\":\"system\",\"subtype\":\"custom_title\",\"systemPayload\":{\"customTitle\":\"Spoof\"}`)}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "s1.jsonl")
			writeSession(t, path, test.records...)
			if got := lookupTitle(t, registry.ObservationIdentity{SessionID: "s1", SessionPath: path, CWD: "", Attributes: nil}); got != test.want {
				t.Fatalf("title = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSessionTitleFromProjectDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("QWEN_HOME", home)
	t.Setenv("QWEN_RUNTIME_DIR", "")
	for _, test := range []struct{ name, cwd, project string }{
		{"ASCII punctuation", "/work/my.project", "-work-my-project"},
		{"BMP characters", "/work/caf\u00e9", "-work-caf-"},
		{"non-BMP character", "/work/\U0001f680", "-work---"},
		{"mixed Unicode", "/work/\u00e9\U0001f680a\U0001f319", "-work----a--"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeSession(t, filepath.Join(home, "projects", test.project, "chats", "s1.jsonl"), titleRecord("s1", "Project title", "auto"))

			if got := lookupTitle(t, registry.ObservationIdentity{SessionID: "s1", SessionPath: "", CWD: test.cwd, Attributes: nil}); got != "Project title" {
				t.Fatalf("title = %q, want %q", got, "Project title")
			}
			if got := lookupTitle(t, registry.ObservationIdentity{SessionID: "missing", SessionPath: "", CWD: test.cwd, Attributes: nil}); got != "" {
				t.Fatalf("missing session title = %q", got)
			}
		})
	}
}
