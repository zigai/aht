package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestClaudeTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("claude"), map[string]bool{
		`{"system":[{"type":"text","text":"You are naming a coding session so the user ca` +
			`n pick it out of a long list of sessions. The title is short."}],"messages":[{"r` +
			`ole":"user","content":"<session>Refine the parser</session>"}]}`: true,
		`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.331"` +
			`}],"messages":[{"role":"user","content":"<session>\nRefine the parser\n</session` +
			`>\n\nWrite the title in the predominant language of the session — a stray word o` +
			`r code token in another language doesn't change it."}]}`: true,
		`{"messages":[{"role":"user","content":"<session>\nRefine the parser\n</session>"}]}`: false,
		`{"messages":[{"role":"user","content":"Write the title in the predominant langua` +
			`ge"}]}`: false,
		`{"system":"Help with code","messages":[{"role":"user","content":"You are naming ` +
			`a coding session so the user can pick it out of a long list of sessions."}]}`: false,
	})
}
