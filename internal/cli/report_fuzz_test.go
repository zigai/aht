package cli

import (
	"strings"
	"testing"
)

func FuzzReportAttributes(f *testing.F) {
	for _, seed := range []string{"key=value", " space =a=b ", "=empty", "missing", "unicode_é=✓"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			return
		}
		attrs, err := parseAttributes([]string{input})
		key, value, found := strings.Cut(input, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			if err == nil {
				t.Fatalf("seed=%q: accepted invalid attribute", input)
			}
			return
		}
		if err != nil || len(attrs) != 1 || attrs[key] != value {
			t.Fatalf("seed=%q: parseAttributes = %#v, %v; want %q=%q", input, attrs, err, key, value)
		}
	})
}
