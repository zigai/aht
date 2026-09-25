package kilo

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestPluginTemplateRendersCleanly(t *testing.T) {
	t.Parallel()

	h := New()
	plan := h.InstallPlan("/usr/local/bin/aht")
	if len(plan.Actions) == 0 {
		t.Fatal("expected at least one install action")
	}
	action, ok := plan.Actions[0].(harness.RenderedFileAction)
	if !ok {
		t.Fatalf("expected harness.RenderedFileAction, got %T", plan.Actions[0])
	}
	rendered := action.Plan.Content
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("rendered kilo template is empty")
	}
	placeholderPattern := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)
	if match := placeholderPattern.FindString(rendered); match != "" {
		t.Fatalf("rendered kilo template contains unresolved placeholder %q:\n%s", match, rendered)
	}
}

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("KILO_CONFIG_DIR", "/tmp/kilo-config")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/ignored-xdg")
	if got := kiloConfigDir(); got != "/tmp/kilo-config" {
		t.Fatalf("expected KILO_CONFIG_DIR to win, got %q", got)
	}
}
