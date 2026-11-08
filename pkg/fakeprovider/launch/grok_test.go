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
		`{"input":[{"role":"user","content":"<system-reminder>\nWrite an ultra-short dash` +
			`board line that captures the AGENT'S REPLY for the last turn only\n</system-remi` +
			`nder>"}]}`: true,
		`{"input":"Generate a session title for the conversation above."}`: false,
		`{"input":"Explain Write an ultra-short dashboard line that captures the AGENT'S ` +
			`REPLY for the last turn only"}`: false,
		`{"tools":[{"type":"function","function":{"name":"shell"}}],"input":"Explain sess` +
			`ion_title"}`: false,
	})
}
