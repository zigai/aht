package kimi

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestKimiACPTitleResponsesDecodeNativeFieldNames(t *testing.T) {
	supported, err := kimiACPHasSessionList(json.RawMessage(`{"agentCapabilities":{"sessionCapabilities":{"list":{}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatal("session/list capability was not detected")
	}

	sessions, err := decodeKimiACPSessions(json.RawMessage(`{"sessions":[{"sessionId":"session-1","title":"Kimi title"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "session-1" || sessions[0].Title != "Kimi title" {
		t.Fatalf("decoded sessions = %#v", sessions)
	}
}

func TestKimiFallbackTitlesRequireOneWorkspaceMatch(t *testing.T) {
	titles := []string{""}
	fallback := kimiSessionTitleFallback{indices: map[int]bool{0: true}}
	fallbackTitles := make(map[int][]string)
	group := &kimiTitleGroup{indicesByID: map[string][]int{"session-1": {0}}}
	applyKimiSessionTitles(group, fallback, []kimiACPSessionInfo{{SessionID: "session-1", Title: "Workspace title"}}, titles, fallbackTitles)
	if err := resolveKimiFallbackTitles(fallbackTitles, titles); err != nil {
		t.Fatal(err)
	}
	if titles[0] != "Workspace title" {
		t.Fatalf("title = %q, want %q", titles[0], "Workspace title")
	}

	titles[0] = ""
	fallbackTitles = make(map[int][]string)
	applyKimiSessionTitles(group, fallback, []kimiACPSessionInfo{{SessionID: "session-1", Title: "First title"}}, titles, fallbackTitles)
	applyKimiSessionTitles(group, fallback, []kimiACPSessionInfo{{SessionID: "session-1", Title: "Second title"}}, titles, fallbackTitles)
	if err := resolveKimiFallbackTitles(fallbackTitles, titles); !errors.Is(err, errKimiSessionMatchesMultipleWorkspaces) {
		t.Fatalf("ambiguous fallback error = %v, want multiple-workspaces sentinel", err)
	}
	if titles[0] != "" {
		t.Fatalf("ambiguous title = %q, want empty", titles[0])
	}
}

func TestKimiSessionPathMustBeInSessionStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", home)

	storedPath := filepath.Join(home, "sessions", "workspace-hash", "session-1")
	if !kimiSessionPathMayUseWorkspaceLookup(storedPath, "session-1") {
		t.Fatalf("session path %q was rejected", storedPath)
	}
	if kimiSessionPathMayUseWorkspaceLookup(filepath.Join(home, "other", "session-1"), "session-1") {
		t.Fatal("session path outside the Kimi store was accepted")
	}
}
