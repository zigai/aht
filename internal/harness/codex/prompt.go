package codex

import (
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

func userPromptParts(body string) []transcript.TextPart {
	body = strings.TrimSpace(body)

	text := body
	if strings.HasPrefix(text, "# AGENTS.md instructions") {
		_, text, _ = strings.Cut(text, "\n")
	}

	_, typed := transcript.SplitPrompt(text)

	return transcript.UserParts(strings.TrimSpace(body[:len(body)-len(typed)]), typed)
}
