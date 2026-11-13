package processinfo

import (
	"os"
	"path/filepath"
	"testing"
)

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
