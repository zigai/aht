package codex

import (
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func TestUserPromptParts(t *testing.T) {
	for _, test := range []struct{ body, typed string }{
		{"# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>rules</INSTRUCTIONS>", ""},
		{"# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>rules</INSTRUCTIONS>\nShip it", "Ship it"},
		{"<environment_context>env</environment_context>\nFix it", "Fix it"},
		{"Fix the parser", "Fix the parser"},
	} {
		var typed strings.Builder

		for _, part := range userPromptParts(test.body) {
			if part.Role == transcript.TypedUserRole {
				typed.WriteString(part.Text)
			}
		}

		if typed.String() != test.typed {
			t.Errorf("typed text for %q = %q, want %q", test.body, typed.String(), test.typed)
		}
	}
}
