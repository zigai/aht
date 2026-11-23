package claude_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/claude"
)

func TestResumeCommandRestoresCodeMode(t *testing.T) {
	t.Parallel()

	plain := []string{"claude", "--resume", "session-1"}
	codeMode := []string{
		"env",
		"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1",
		"claude",
		"--resume",
		"session-1",
	}

	tests := []struct {
		name       string
		transcript []string
		want       []string
	}{
		{
			name: "started in code mode",
			transcript: []string{
				`{"type":"mode","mode":"normal","sessionId":"session-1"}`,
				`{"type":"repl-mode","replMode":true,"sessionId":"session-1"}`,
				`{"type":"user","message":{"role":"user","content":"hello"}}`,
			},
			want: codeMode,
		},
		{
			name: "code mode turned off later",
			transcript: []string{
				`{"type":"repl-mode","replMode":true,"sessionId":"session-1"}`,
				`{"type":"repl-mode","replMode":false,"sessionId":"session-1"}`,
			},
			want: plain,
		},
		{
			name: "no repl mode record",
			transcript: []string{
				`{"type":"mode","mode":"normal","sessionId":"session-1"}`,
				`{"type":"user","message":{"role":"user","content":"hello"}}`,
			},
			want: plain,
		},
		{
			name: "marker quoted in a message",
			transcript: []string{
				`{"type":"user","message":{"role":"user","content":"{\"type\":\"repl-mode\",\"replMode\":true}"}}`,
			},
			want: plain,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "session-1.jsonl")
			if err := os.WriteFile(
				path,
				[]byte(strings.Join(test.transcript, "\n")+"\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}

			if got := claude.New().ResumeCommand("session-1", path); !slices.Equal(got, test.want) {
				t.Fatalf("ResumeCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResumeCommandWithoutTranscript(t *testing.T) {
	t.Parallel()

	want := []string{"claude", "--resume", "session-1"}

	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing.jsonl")} {
		if got := claude.New().ResumeCommand("session-1", path); !slices.Equal(got, want) {
			t.Fatalf("ResumeCommand(%q) = %q, want %q", path, got, want)
		}
	}
}
