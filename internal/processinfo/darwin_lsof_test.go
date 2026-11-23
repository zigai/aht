package processinfo

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDarwinLsofCWD(t *testing.T) {
	t.Parallel()

	output := "n/orphan\np421\nfcwd\nn/work/first\np422\nfcwd\np423\nfcwd\nn/work/second path\nlsof: PID 422 exited\n"
	want := map[int]string{421: "/work/first", 423: "/work/second path"}

	got, err := parseDarwinLsofCWD([]byte(output))
	if err != nil {
		t.Fatal(err)
	}

	if !maps.Equal(got, want) {
		t.Fatalf("working directories = %v, want %v", got, want)
	}
}

func TestParseDarwinLsofCWDRejectsInvalidOutput(t *testing.T) {
	t.Parallel()

	for _, output := range []string{"", "pbad\nn/work/first\n", "p421\nfcwd\nn\n"} {
		if _, err := parseDarwinLsofCWD([]byte(output)); err == nil {
			t.Errorf("accepted output without a valid working directory: %q", output)
		}
	}
}

func TestNormalizeDarwinTTY(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"  ":           "",
		" ?? ":         "",
		"-":            "",
		" ttys005 ":    "/dev/ttys005",
		"/dev/ttys006": "/dev/ttys006",
	} {
		if got := normalizeDarwinTTY(raw); got != want {
			t.Errorf("TTY for %q = %q, want %q", raw, got, want)
		}
	}
}

func TestDarwinLsofPathRestoresEscapedBytes(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"/work/plain":               "/work/plain",
		"/work/^[[31mcolored^[[0m":  "/work/\x1b[31mcolored\x1b[0m",
		`/work/tab\there`:           "/work/tab\there",
		`/work/caf\xc3\xa9`:         "/work/café",
		"/work/caret^ and \\ slash": "/work/caret^ and \\ slash",
		`/work/short\x4`:            `/work/short\x4`,
	} {
		if got := darwinLsofPath(name); got != want {
			t.Errorf("path for lsof name %q = %q, want %q", name, got, want)
		}
	}
}

func TestDarwinLsofPathKeepsExistingEscapeLikeName(t *testing.T) {
	t.Parallel()

	literal := filepath.Join(t.TempDir(), "^[name")
	if err := os.Mkdir(literal, 0o700); err != nil {
		t.Fatal(err)
	}

	if got := darwinLsofPath(literal); got != literal {
		t.Fatalf("path for existing directory %q = %q", literal, got)
	}
}
