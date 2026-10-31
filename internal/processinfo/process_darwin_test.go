//go:build darwin

package processinfo

import "testing"

func TestParseDarwinPSForegroundProcessGroup(t *testing.T) {
	t.Parallel()

	rows, err := parseDarwinPS(
		"123 1 123 123 ttys001 /usr/local/bin/codex codex --model test\n124 1 124 123 ttys001 /bin/sh sh\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	foreground := rows[123]
	if !foreground.Foreground || foreground.ProcessGroupID != 123 || foreground.TTY != "ttys001" ||
		foreground.Executable != "/usr/local/bin/codex" {
		t.Fatalf("foreground row = %#v", foreground)
	}

	if rows[124].Foreground {
		t.Fatalf("background row marked foreground: %#v", rows[124])
	}
}
