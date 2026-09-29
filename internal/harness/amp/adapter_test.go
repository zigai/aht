package amp

import (
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

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
	if def.ID != registry.Harness("amp") {
		t.Fatalf("expected ID %q, got %q", registry.Harness("amp"), def.ID)
	}
	if !def.Capabilities.SessionStart || !def.Capabilities.SessionEnd || !def.Capabilities.RunningIdle || !def.Capabilities.WaitingPermission {
		t.Fatalf("unexpected capabilities: %+v", def.Capabilities)
	}
}
