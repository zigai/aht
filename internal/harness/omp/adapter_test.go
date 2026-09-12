package omp

import (
	"strings"
	"testing"

	"github.com/zigai/aht/internal/harness"
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
		t.Fatal("rendered omp template is empty")
	}
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		t.Fatalf("rendered omp template contains unresolved placeholders:\n%s", rendered)
	}
}

func TestAgentDirUsesExplicitEmptyProfile(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("OMP_CODING_AGENT_DIR", "")
	t.Setenv("OMP_CONFIG_DIR", "/tmp/omp")
	t.Setenv("OMP_PROFILE", "")
	if got := ompAgentDir(); got != "/tmp/omp/agent" {
		t.Fatalf("expected explicit empty OMP_PROFILE to select default, got %q", got)
	}
}
