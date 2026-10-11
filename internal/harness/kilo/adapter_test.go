package kilo

import "testing"

func TestConfigDirOverride(t *testing.T) {
	t.Setenv("KILO_CONFIG_DIR", "/tmp/kilo-config")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/ignored-xdg")
	if got := kiloConfigDir(); got != "/tmp/kilo-config" {
		t.Fatalf("expected KILO_CONFIG_DIR to win, got %q", got)
	}
}
