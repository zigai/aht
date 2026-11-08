package launch

import (
	"encoding/json"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func checkTitleRequests(t *testing.T, id registry.Harness, cases map[string]bool) {
	t.Helper()

	match, err := TitleRequestFor(id)
	if err != nil {
		t.Fatal(err)
	}

	for body, want := range cases {
		var value map[string]any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}

		if got := match(value); got != want {
			t.Errorf("title request %s = %t, want %t", body, got, want)
		}
	}
}
