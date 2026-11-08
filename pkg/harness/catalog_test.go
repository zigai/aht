package harness_test

import (
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/harness"
)

func TestParseMatchesNamesWithoutPunctuation(t *testing.T) {
	t.Parallel()

	for _, adapter := range catalog.All() {
		def := adapter.Definition()
		for _, name := range append([]string{string(def.ID)}, def.Aliases...) {
			for _, separator := range []string{" ", ".", "-", "_", ":", "!"} {
				input := strings.Join(strings.Split(name, ""), separator)

				got, err := harness.Parse(input)
				if err != nil || got != def.ID {
					t.Errorf("Parse(%q)=%q,%v; want %q", input, got, err, def.ID)
				}
			}
		}
	}
}
