//go:build darwin

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	darwinLabel     = "dev.zigai.aht.observer"
	darwinPlistName = "dev.zigai.aht.observer.plist"
)

var _ backend = (*darwinBackend)(nil)

type darwinBackend struct {
	path     string
	rendered string
	domain   string
}

// RenderLaunchAgent returns the exact managed LaunchAgent plist for options.
func RenderLaunchAgent(options Options) (string, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return "", err
	}
	args := []string{normalized.Binary, "--store", normalized.StorePath, "manage", "tracker", "run", "--interval", normalized.Interval.String(), "--grace-period", normalized.GracePeriod.String(), "--quiet"}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!-- " + ManagedMarker + " -->\n<!-- version: " + strconv.Itoa(ManagedVersion) + " -->\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\"><dict>\n")
	b.WriteString("<key>Label</key><string>" + xmlEscape(darwinLabel) + "</string>\n")
	b.WriteString("<key>ProgramArguments</key><array>\n")
	for _, arg := range args {
		b.WriteString("<string>" + xmlEscape(arg) + "</string>\n")
	}
	b.WriteString("</array>\n<key>RunAtLoad</key><true/>\n")
	b.WriteString("<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n")
	b.WriteString("</dict></plist>\n")
	return b.String(), nil
}

func platformBackend(options Options) (backend, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	rendered, err := RenderLaunchAgent(normalized)
	if err != nil {
		return nil, err
	}
	path, err := launchAgentPath()
	if err != nil {
		return nil, err
	}
	return &darwinBackend{path: path, rendered: rendered, domain: fmt.Sprintf("gui/%d", os.Getuid())}, nil
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", darwinPlistName), nil
}

func (b *darwinBackend) describe() Result {
	return Result{Platform: "darwin", Manager: "launchctl", ManagedPath: b.path, ManagedVersion: ManagedVersion, Path: b.path, Version: ManagedVersion, Installed: false, Current: false, Running: false, Changed: false, Message: ""}
}
func (b *darwinBackend) content() string                               { return b.rendered }
func (b *darwinBackend) reload(context.Context, CommandExecutor) error { return nil }
func (b *darwinBackend) load(ctx context.Context, executor CommandExecutor) error {
	if output, err := executor.Run(ctx, "launchctl", "bootstrap", b.domain, b.path); err != nil {
		return wrapManagerError("launchctl bootstrap observer", output, err)
	}
	return nil
}

func (b *darwinBackend) restart(ctx context.Context, executor CommandExecutor) error {
	if err := b.unload(ctx, executor); err != nil {
		return err
	}
	return b.load(ctx, executor)
}

func (b *darwinBackend) unload(ctx context.Context, executor CommandExecutor) error {
	output, err := executor.Run(ctx, "launchctl", "bootout", b.domain, b.path)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("unload launchd service: %w", err)
	}
	if err != nil && !managerMissing(output) {
		return wrapManagerError("launchctl bootout observer", output, err)
	}
	return nil
}

func (b *darwinBackend) running(ctx context.Context, executor CommandExecutor) (bool, string, error) {
	output, err := executor.Run(ctx, "launchctl", "print", b.domain+"/"+darwinLabel)
	if err == nil {
		for line := range strings.Lines(string(output)) {
			if state, ok := strings.CutPrefix(strings.TrimSpace(line), "state = "); ok {
				return state == "running", state, nil
			}
		}
		return false, "", fmt.Errorf("%w: launchctl did not report a state", errInstalledArguments)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, "", fmt.Errorf("checking launchd service status: %w", err)
	}
	// Missing-service diagnostics are distinct from an unavailable GUI domain,
	// insufficient privileges, or a failure to execute launchctl itself.
	if _, exited := errors.AsType[*exec.ExitError](err); exited && strings.Contains(strings.ToLower(string(output)), "could not find service") {
		return false, "not running", nil
	}
	return false, "", wrapManagerError("checking launchd service status", output, err)
}

func xmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	return strings.ReplaceAll(value, "'", "&apos;")
}
