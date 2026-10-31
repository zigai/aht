package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func FuzzDecodeTOMLLayers(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(``),
		[]byte("[ui]\nsort = \"created\"\nsort_desc = true\n"),
		[]byte("[filter]\nignore_paths = [\"/tmp/*\"]\n[tracker]\ninterval = \"5s\"\n"),
		[]byte("[detection]\nscreen_inspection = false\n"),
		[]byte("[ui]\nunknown = 1\n"),
		[]byte("[ui\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxConfigFileSize {
			return
		}

		base := Defaults()
		if err := DecodeTOML(data, &base); err != nil {
			return
		}

		before := base
		if err := DecodeTOML(nil, &base); err != nil {
			t.Fatalf("seed=%q: empty layer failed: %v", data, err)
		}

		if diff := cmp.Diff(before, base); diff != "" {
			t.Fatalf("seed=%q: empty layer changed config (-before +after):\n%s", data, diff)
		}

		if err := DecodeTOML([]byte("[ui]\nsort = \"created\"\n"), &base); err != nil {
			t.Fatalf("seed=%q: later valid layer failed: %v", data, err)
		}

		if base.UI.Sort != "created" {
			t.Fatalf("seed=%q: later sort did not win: %q", data, base.UI.Sort)
		}
	})
}
