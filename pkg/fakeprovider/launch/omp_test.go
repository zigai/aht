package launch

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestOmpTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("omp"), map[string]bool{
		`{"messages":[{"role":"system","content":"You name coding-agent sessions. Write a` +
			` title."},{"role":"user","content":"Refine the parser"}]}`: true,
		`{"messages":[{"role":"user","content":"<title-request>Write a title.</title-requ` +
			`est>"}]}`: true,
		`{"messages":[{"role":"user","content":"Explain <title-request> and title parsing` +
			`"}]}`: false,
		`{"messages":[{"role":"user","content":"<title-request>incomplete"}]}`: false,
		`{"messages":[{"role":"assistant","content":"<title-request>Write a title.</title` +
			`-request>"}]}`: false,
		`{"messages":[{"role":"user","content":"<title-request>Write a title.</title-requ` +
			`est>"},{"role":"assistant","content":"Named it"},{"role":"user","content":"Conti` +
			`nue"}]}`: false,
	})
}
