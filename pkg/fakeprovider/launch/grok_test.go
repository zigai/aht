package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestGrokTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("grok"), map[string]bool{
		`{"tools":[{"type":"function","function":{"name":"session_title"}}],"input":[{"ro` +
			`le":"user","content":"Refine the parser"}]}`: true,
		`{"tools":[{"name":"session_title"}],"messages":[{"role":"user","content":"Refine` +
			` the parser"}]}`: true,
		`{"input":"Explain the phrase Generate a session title"}`: false,
		`{"input":[{"role":"user","content":"<system-reminder>\nGenerate a session title ` +
			`for the conversation above. Use the whole conversation.\n</system-reminder>"}]}`: true,
		`{"input":[{"role":"system","content":"Write an ultra-short dashboard line that ` +
			`captures the AGENT'S REPLY to the user's message. Focus on what the agent ` +
			`concluded, answered, recommended, or delivered."},{"role":"user","content":` +
			`"<user_message>Refine the parser</user_message><agent_reply>Done</agent_reply>"}]}`: true,
		`{"input":[{"role":"user","content":"Write an ultra-short dashboard line that ` +
			`captures the AGENT'S REPLY to the user's message."}]}`: false,
		`{"input":[{"role":"system","content":"Explain the phrase Write an ultra-short ` +
			`dashboard line that captures the AGENT'S REPLY to the user's message."}]}`: false,
		`{"input":"Generate a session title for the conversation above."}`: false,
		`{"tools":[{"type":"function","function":{"name":"shell"}}],"input":"Explain sess` +
			`ion_title"}`: false,
	})
}
