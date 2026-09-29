package omp

import "testing"

func TestAgentDirUsesExplicitEmptyProfile(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("OMP_CODING_AGENT_DIR", "")
	t.Setenv("OMP_CONFIG_DIR", "/tmp/omp")
	t.Setenv("OMP_PROFILE", "")
	if got := ompAgentDir(); got != "/tmp/omp/agent" {
		t.Fatalf("expected explicit empty OMP_PROFILE to select default, got %q", got)
	}
}
