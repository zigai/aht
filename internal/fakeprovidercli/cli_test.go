package fakeprovidercli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
)

func unsupportedHarnessName(t *testing.T) string {
	t.Helper()

	for _, adapter := range catalog.All() {
		if _, ok := adapter.(harness.Launchable); !ok {
			return string(adapter.Definition().ID)
		}
	}

	t.Skip("every catalog harness has a launch plan")

	return ""
}

func launchableHarnessName(t *testing.T) string {
	t.Helper()

	names := launchableHarnessNames()
	if len(names) == 0 {
		t.Skip("no catalog harness has a launch plan")
	}

	return names[0]
}

func TestStartRejectsBadInvocationBeforeCreatingRoot(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	launchable := launchableHarnessName(t)

	cases := []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "missing harness", args: nil, wantText: "accepts 1 arg"},
		{
			name:     "extra argument",
			args:     []string{"no-such-harness", "another-missing"},
			wantText: "accepts 1 arg",
		},
		{name: "unknown harness", args: []string{"no-such-harness"}, wantText: "unknown harness"},
		{
			name:     "unsupported harness",
			args:     []string{unsupportedHarnessName(t)},
			wantText: "does not support fake provider launches",
		},
		{name: "aht missing from PATH", args: []string{launchable}, wantText: "--no-integration"},
		{
			name:     "aht path that does not exist",
			args:     []string{launchable, "--aht", filepath.Join(t.TempDir(), "aht")},
			wantText: "find aht executable",
		},
		{
			name:     "aht with no integration",
			args:     []string{launchable, "--aht", "aht", "--no-integration"},
			wantText: "cannot be combined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			args := append([]string{"start", "--root", root}, tc.args...)

			var stdout, stderr bytes.Buffer

			code := execute(t.Context(), args, &stdout, &stderr)
			if code != exitCodeUsage {
				t.Fatalf(
					"%v exit code = %d, want %d; stderr=%q",
					args,
					code,
					exitCodeUsage,
					stderr.String(),
				)
			}

			if !strings.Contains(stderr.String(), tc.wantText) {
				t.Fatalf(
					"%v stderr = %q, want it to contain %q",
					args,
					stderr.String(),
					tc.wantText,
				)
			}

			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%v created root before validation: %v", args, err)
			}
		})
	}
}

func TestServeRejectsBadArgumentsBeforeListening(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{name: "positional argument", args: []string{"extra"}},
		{name: "address without port", args: []string{"--address", "127.0.0.1"}},
		{name: "port above range", args: []string{"--address", "127.0.0.1:65536"}},
		{name: "non-numeric port", args: []string{"--address", "127.0.0.1:http"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"serve"}, tc.args...)

			var stdout, stderr bytes.Buffer

			code := execute(t.Context(), args, &stdout, &stderr)
			if code != exitCodeUsage {
				t.Fatalf("%v exit code = %d, want %d", args, code, exitCodeUsage)
			}

			if stdout.Len() != 0 {
				t.Fatalf("%v printed output before validation: %q", args, stdout.String())
			}

			if stderr.Len() == 0 {
				t.Fatalf("%v omitted stderr diagnostic", args)
			}
		})
	}
}
