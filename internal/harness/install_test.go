package harness_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestRenderScriptTemplateExpandsQueueAndLeavesNoPlaceholders(t *testing.T) {
	t.Parallel()

	sampleTemplate := "{{MANAGED_MARKER}}\nconst id = {{INTEGRATION_ID}};\nconst v = {{INTEGRATION_VERSION}};\nconst bin = {{BINARY}};\nconst src = {{SOURCE}};\n{{TYPESCRIPT_QUEUE}}\nconst format = {nested:{}};\n"
	rendered := harness.RenderScriptTemplate(sampleTemplate, "test-integration", "/bin/test-aht", "test-source", 42)

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
