package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestKiloTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("kilo"), map[string]bool{
		`{"messages":[{"role":"system","content":"You are a title generator. You output O` +
			`NLY a thread title. Nothing else."},{"role":"user","content":"Refine the parser"` +
			`}]}`: true,
		`{"messages":[{"role":"system","content":"Help with code"},{"role":"user","conten` +
			`t":"Explain the phrase You are a title generator."}]}`: false,
		`{"messages":[{"role":"user","content":"You are a title generator. Explain the wo` +
			`rding."}]}`: false,
	})
}
