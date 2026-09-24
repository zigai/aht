package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/internal/config"
)

func TestMain(m *testing.M) {
	tempDir, err := os.MkdirTemp("", "aht-cli-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for tests: %v\n", err)
		os.Exit(1)
	}

	emptyConfig := filepath.Join(tempDir, "default-test-config.toml")
	if err := os.WriteFile(emptyConfig, []byte(""), 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		fmt.Fprintf(os.Stderr, "failed to write empty config for tests: %v\n", err)
		os.Exit(1)
	}
	_ = os.Setenv(config.ConfigEnv, emptyConfig)
	_ = os.Setenv("XDG_CONFIG_DIRS", filepath.Join(tempDir, "system"))
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, "user"))

	code := m.Run()
	_ = os.RemoveAll(tempDir)
	os.Exit(code)
}
