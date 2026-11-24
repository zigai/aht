package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestOmpTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("omp"), map[string]bool{
		`{"messages":[{"role":"system","content":"You name coding-agent sessions. Write a` +
			` title."},{"role":"user","content":"<user>\nRefine the parser\n</user>"}]}`: true,
		`{"messages":[{"role":"system","content":"You head coding-agent session titles with` +
			` a small card. Pick the card for the session title in the next message."},{"role"` +
			`:"system","content":"Output only the title wrapped in ` + "`<title>`" + ` and ` +
			"`</title>`" + ` tags, with nothing before or after."},{"role":"user","content":"` +
			`<user>\nFake session\n</user>"}]}`: true,
		`{"messages":[{"role":"system","content":"# Task\nName the plan in the next user ` +
			`message:\n\nOutput only the title wrapped in ` + "`<title>`" + ` and ` +
			"`</title>`" + ` tags, with nothing before or after."},{"role":"user","content":"` +
			`<user>\nPlan body\n</user>"}]}`: true,
		`{"messages":[{"role":"user","content":"<title-request>Write a title.</title-requ` +
			`est>"}]}`: true,
		`{"messages":[{"role":"user","content":"<composer-prediction>\nPredict the user's` +
			` next message in this conversation.\n</composer-prediction>"}]}`: true,
		`{"messages":[{"role":"user","content":"<user>\nNormal user message\n</user>"}]}`: false,
		`{"messages":[{"role":"user","content":"Explain <title-request> and title parsing` +
			`"}]}`: false,
		`{"messages":[{"role":"user","content":"Explain <composer-prediction> prompts"}]}`: false,
		`{"messages":[{"role":"user","content":"<composer-prediction>incomplete"}]}`:       false,
		`{"messages":[{"role":"user","content":"<title-request>incomplete"}]}`:             false,
		`{"messages":[{"role":"assistant","content":"<title-request>Write a title.</title` +
			`-request>"}]}`: false,
		`{"messages":[{"role":"assistant","content":"<composer-prediction>Next</composer-` +
			`prediction>"}]}`: false,
		`{"messages":[{"role":"user","content":"<title-request>Write a title.</title-requ` +
			`est>"},{"role":"assistant","content":"Named it"},{"role":"user","content":"Conti` +
			`nue"}]}`: false,
		`{"messages":[{"role":"user","content":"<composer-prediction>Next</composer-predi` +
			`ction>"},{"role":"assistant","content":"ok"},{"role":"user","content":"Continue"` +
			`}]}`: false,
	})
}
