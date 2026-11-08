package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDroidTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("droid"), map[string]bool{
		`{"system":"Provider preamble\nYou are a helper that generates concise session ti` +
			`tles for a session picker.","messages":[{"role":"user","content":"Refine the par` +
			`ser"}]}`: true,
		`{"messages":[{"role":"user","content":"You are a helper that generates concise s` +
			`ession titles for a session picker."}]}`: false,
	})
}
