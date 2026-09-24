//go:build integration

package install

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func TestIntegrationClineCLIDiscoversManagedPlugin(t *testing.T) {
	cline, err := exec.LookPath("cline")
	if err != nil {
		t.Skip("Cline CLI is not installed")
	}

	home := t.TempDir()
	clineDir := filepath.Join(home, ".cline")
	t.Setenv("HOME", home)
	t.Setenv("CLINE_DIR", clineDir)
	result, err := Run(Options{Harness: registry.Harness("cline"), Binary: testInstallBinary})
	if err != nil {
		t.Fatalf("installing Cline plugin: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, cline, "config", "plugins", "--json").Output()
	if err != nil {
		t.Fatalf("asking Cline to discover plugins: %v", err)
	}
	var plugins []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(output, &plugins); err != nil {
		t.Fatalf("parsing Cline plugin discovery output %q: %v", output, err)
	}
	want := filepath.Join(result.Path, "index.js")
	for _, plugin := range plugins {
		if plugin.Path == want {
			return
		}
	}
	t.Fatalf("Cline did not discover managed plugin %s: %#v", want, plugins)
}
