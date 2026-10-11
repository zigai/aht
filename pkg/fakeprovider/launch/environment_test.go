package launch

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestMergeEnvironmentLaterLayersWin(t *testing.T) {
	t.Parallel()
	merged, err := mergeEnvironment(
		[]string{"A=common", "B=common"},
		[]string{"B=plan", "C=plan"},
		[]string{"C=options", "D=options"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A=common", "B=plan", "C=options", "D=options"}
	if !slices.Equal(merged, want) {
		t.Fatalf("mergeEnvironment() = %q, want %q", merged, want)
	}
}

func TestMergeEnvironmentRejectsMalformedEntries(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"NOEQUALS", "=value"} {
		if _, err := mergeEnvironment([]string{entry}); !errors.Is(err, errMalformedEnvironment) {
			t.Errorf("mergeEnvironment(%q) error = %v, want errMalformedEnvironment", entry, err)
		}
	}
}

func TestCommonEnvironmentOmitsCallerCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "caller-secret")
	t.Setenv("ANTHROPIC_BASE_URL", "https://caller.example")
	t.Setenv("PATH", "/caller/bin")
	root := filepath.Join(t.TempDir(), "root")
	env := commonEnvironment(root, filepath.Join(root, "sessions.json"))
	for _, entry := range env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") || strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") {
			t.Fatalf("common environment inherited %q", entry)
		}
	}
	if got, _ := envValue(env, "PATH"); got != "/caller/bin" {
		t.Fatalf("PATH = %q, want the caller's PATH passed through", got)
	}
}
