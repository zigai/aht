package agentstate

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestManifestDetailIsIndependentOfRuleName(t *testing.T) {
	t.Parallel()
	manifest, err := ParseManifest([]byte(`version = 1
agent = "claude"
[[rules]]
id = "arbitrary_name"
state = "waiting"
detail = "question"
all = ["choose"]
`), registry.Harness("claude"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := NormalizeSnapshot("choose", "")
	decision := manifest.Evaluate(snapshot)
	if decision.Detail != registry.DetailQuestion {
		t.Fatalf("detail = %s", decision.Detail)
	}
	if !reflect.DeepEqual(decision, manifest.Inspect(snapshot).Decision) {
		t.Fatal("inspect and evaluate disagree")
	}
}

func TestManifestRejectsDetailForDifferentActivity(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"running", "permission"}, {"waiting", "usage_limit"}, {"failed", "question"}, {"waiting", "plan"}} {
		data := "version = 1\nagent = \"claude\"\n[[rules]]\nid = \"test\"\nstate = \"" + pair[0] + "\"\ndetail = \"" + pair[1] + "\"\nall = [\"x\"]"
		if _, err := ParseManifest([]byte(data), registry.Harness("claude")); err == nil {
			t.Fatalf("accepted %v", pair)
		}
	}
}
