package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestHermesTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("hermes"), map[string]bool{
		`{"messages":[{"role":"system","content":"You name chat sessions. Given the user'` +
			`s opening message, write a title that lets them find this conversation again in ` +
			`a list."},{"role":"user","content":"Refine the parser"}]}`: true,
		`{"messages":[{"role":"user","content":"You name chat sessions. Explain this inst` +
			`ruction."}]}`: false,
	})
}
