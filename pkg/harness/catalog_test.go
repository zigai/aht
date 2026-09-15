package harness_test

import (
	"slices"
	"testing"

	"github.com/zigai/aht/pkg/harness"
	"github.com/zigai/aht/pkg/registry"
)

func TestProcessNames(t *testing.T) {
	t.Parallel()

	names := harness.ProcessNames(registry.HarnessCodex)
	if !slices.Contains(names, "codex") {
		t.Errorf("ProcessNames(codex) = %v, want codex", names)
	}
}

func TestFromCommandResolvesProcessNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    registry.Harness
	}{
		{name: "openclaw", command: "openclaw", want: registry.HarnessOpenClaw},
		{name: "openclaw path", command: "/usr/local/bin/openclaw", want: registry.HarnessOpenClaw},
		{name: "hermes", command: "hermes", want: registry.HarnessHermes},
		{name: "hermes agent", command: "hermes-agent", want: registry.HarnessHermes},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := harness.FromCommand(test.command)
			if !ok || got != test.want {
				t.Fatalf("FromCommand(%q) = (%q, %t), want (%q, true)", test.command, got, ok, test.want)
			}
		})
	}
}

func TestParseMatchesNamesWithoutPunctuation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  registry.Harness
	}{
		{name: "claude code space", value: "claude code", want: registry.HarnessClaude},
		{name: "claude code dot", value: "claude.code", want: registry.HarnessClaude},
		{name: "codex bang", value: "codex!", want: registry.HarnessCodex},
		{name: "kimi code space", value: "kimi code", want: registry.HarnessKimiCode},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := harness.Parse(test.value)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("Parse(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
