package harness_test

import (
	"runtime"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestHomeDir(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
	default:
		t.Skipf("home directory fallback is platform specific on %s", runtime.GOOS)
	}
	home := t.TempDir()

	for _, test := range []struct {
		name string
		home string
		want string
	}{
		{name: "uses HOME", home: home, want: home},
		{name: "trims whitespace around HOME", home: "  " + home + "\t\n", want: home},
		{name: "unset HOME and no platform home", home: "", want: ""},
		{name: "blank HOME and no platform home", home: " \t ", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.home)
			t.Setenv("USERPROFILE", "")
			if got := harness.HomeDir(); got != test.want {
				t.Fatalf("HomeDir() with HOME=%q = %q, want %q", test.home, got, test.want)
			}
		})
	}
}
