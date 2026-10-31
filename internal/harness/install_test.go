package harness_test

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestRenderScriptTemplateExpandsQueueAndLeavesNoPlaceholders(t *testing.T) {
	t.Parallel()

	sampleTemplate := "{{MANAGED_MARKER}}\nconst id = {{INTEGRATION_ID}};\nconst v = " +
		"{{INTEGRATION_VERSION}};\nconst bin = {{BINARY}};\nconst src " +
		"= {{SOURCE}};\n{{TYPESCRIPT_QUEUE}}\nconst format = " +
		"{nested:{}};\n"
	rendered := harness.RenderScriptTemplate(
		sampleTemplate,
		"test-integration",
		"/bin/test-aht",
		"test-source",
		42,
	)

	placeholderPattern := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)
	if match := placeholderPattern.FindString(rendered); match != "" {
		t.Fatalf("rendered template contains unresolved placeholder %q:\n%s", match, rendered)
	}

	for _, expected := range []string{
		harness.ManagedMarker,
		`const id = test-integration;`,
		`const v = 42;`,
		`const bin = "/bin/test-aht";`,
		`const src = "test-source";`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered template missing expected fragment %q", expected)
		}
	}
}

func TestShellQuoteRecoversArguments(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(property *rapid.T) {
		words := rapid.SliceOf(rapid.StringOf(rapid.Rune().Filter(func(character rune) bool {
			return character != 0
		}))).Draw(property, "arguments")
		quoted := make([]string, len(words))

		var expected []byte

		for index, word := range words {
			quoted[index] = harness.ShellQuote(word)
			expected = append(expected, word...)
			expected = append(expected, 0)
		}

		script := "set -- " + strings.Join(
			quoted,
			" ",
		) + "; for argument do printf '%s\\000' \"$argument\"; done"

		output, err := exec.CommandContext(property.Context(), "sh", "-c", script).Output()
		if err != nil {
			property.Fatalf("shell rejected arguments %q: %v", words, err)
		}

		if !bytes.Equal(output, expected) {
			property.Fatalf("shell arguments = %q, want %q", output, expected)
		}
	})
}
