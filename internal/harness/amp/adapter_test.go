package amp

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
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
		t.Fatal("rendered amp template is empty")
	}
	placeholderPattern := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)
	if match := placeholderPattern.FindString(rendered); match != "" {
		t.Fatalf("rendered amp template contains unresolved placeholder %q:\n%s", match, rendered)
	}
	if !strings.Contains(rendered, "AHT_INTEGRATION_ID=amp") {
		t.Fatalf("expected AHT_INTEGRATION_ID=amp in rendered template:\n%s", rendered)
	}
	if !strings.Contains(rendered, `"report", "amp"`) {
		t.Fatalf("expected report amp in rendered template:\n%s", rendered)
	}
}

func TestResumeCommand(t *testing.T) {
	t.Parallel()

	h := New()
	if cmd := h.ResumeCommand("T-12345", ""); len(cmd) != 4 || cmd[0] != "amp" || cmd[1] != "threads" || cmd[2] != "continue" || cmd[3] != "T-12345" {
		t.Fatalf("unexpected resume command: %v", cmd)
	}
	if cmd := h.ResumeCommand("", ""); cmd != nil {
		t.Fatalf("expected nil for empty session ID, got: %v", cmd)
	}
}

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("AMP_CONFIG_DIR", "/tmp/amp-config")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/ignored-xdg")
	if got := ampConfigDir(); got != "/tmp/amp-config" {
		t.Fatalf("expected AMP_CONFIG_DIR to win, got %q", got)
	}
}

func TestConfigDirXDG(t *testing.T) {
	t.Setenv("AMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/custom-xdg")
	if got := ampConfigDir(); got != "/tmp/custom-xdg/amp" {
		t.Fatalf("expected XDG_CONFIG_HOME/amp, got %q", got)
	}
}

func TestDefinition(t *testing.T) {
	t.Parallel()

	h := New()
	def := h.Definition()
	if def.ID != registry.HarnessAmp {
		t.Fatalf("expected ID %q, got %q", registry.HarnessAmp, def.ID)
	}
	if !def.Capabilities.SessionStart || !def.Capabilities.SessionEnd || !def.Capabilities.RunningIdle || !def.Capabilities.WaitingPermission {
		t.Fatalf("unexpected capabilities: %+v", def.Capabilities)
	}
}
