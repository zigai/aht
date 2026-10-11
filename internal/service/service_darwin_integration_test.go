//go:build darwin && integration

package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPlutilAcceptsManagedLaunchAgent(t *testing.T) {
	validator, err := exec.LookPath("plutil")
	if err != nil {
		t.Fatalf("service validation requires plutil: %v", err)
	}
	plist, err := RenderLaunchAgent(Options{
		Binary:      "/usr/bin/true",
		StorePath:   filepath.Join(t.TempDir(), "sessions.json"),
		Interval:    3 * time.Second,
		GracePeriod: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("render launch agent: %v", err)
	}
	path := filepath.Join(t.TempDir(), darwinPlistName)
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		t.Fatalf("write launch agent: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, validator, "-lint", path).CombinedOutput()
	if err != nil {
		t.Fatalf("plutil rejected managed launch agent: %v\n%s", err, output)
	}
}
