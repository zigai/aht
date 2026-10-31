package cline

import "testing"

func TestSessionDataDirOverride(t *testing.T) {
	t.Setenv("CLINE_SESSION_DATA_DIR", "/tmp/cline-sessions")

	want := "/tmp/cline-sessions/session-1/session-1.messages.json"
	if got := clineSessionPath("session-1"); got != want {
		t.Fatalf("expected CLINE_SESSION_DATA_DIR override, got %q", got)
	}
}
