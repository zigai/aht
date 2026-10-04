//go:build linux && integration

package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemdAnalyzeAcceptsManagedUnit(t *testing.T) {
	validator, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Fatalf("service validation requires systemd-analyze: %v", err)
	}
	unit, err := RenderSystemdUnit(Options{
		Binary:      "/bin/true",
		StorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		Interval:    3 * time.Second,
		GracePeriod: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("render systemd unit: %v", err)
	}
	path := filepath.Join(t.TempDir(), linuxUnitName)
	if err := os.WriteFile(path, []byte(unit), 0o600); err != nil {
		t.Fatalf("write systemd unit: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, validator, "--user", "verify", path).CombinedOutput()
	if err != nil {
		t.Fatalf("systemd-analyze rejected managed unit: %v\n%s", err, output)
	}
}
