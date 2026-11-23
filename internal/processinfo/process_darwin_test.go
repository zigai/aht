//go:build darwin

package processinfo

import "testing"

func TestParseDarwinPSForegroundProcessGroup(t *testing.T) {
	t.Parallel()

	rows, err := parseDarwinPS(
		"123 1 123 123 ttys001\n124 1 124 123 ttys001\n125 1 125 -1 ??\n126 1 126 -1 -\n127 1 127 127 /dev/ttys002\n",
	)
	if err != nil {
		t.Fatal(err)
	}

	foreground := rows[123]
	if !foreground.Foreground || foreground.ProcessGroupID != 123 ||
		foreground.TTY != "/dev/ttys001" {
		t.Fatalf("foreground row = %#v", foreground)
	}

	if rows[124].Foreground {
		t.Fatalf("background row marked foreground: %#v", rows[124])
	}

	for pid, want := range map[int]string{125: "", 126: "", 127: "/dev/ttys002"} {
		if got := rows[pid].TTY; got != want {
			t.Errorf("TTY for process %d = %q, want %q", pid, got, want)
		}
	}
}
